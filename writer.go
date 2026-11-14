package imports

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// tableWriter writes a table in one of the two formats: a heading row, then the
// rows. The template, the report of the refused rows and the export all go
// through it.
type tableWriter interface {
	Header(cells []string) error
	Row(cells []string) error
	Close() error
}

// newTableWriter returns the writer of a format over w. The sheet is the name
// of the one tab of an XLSX.
func newTableWriter(format string, w io.Writer, sheet string) (tableWriter, error) {
	switch format {
	case FormatCSV:
		return newCSVWriter(w), nil
	case FormatXLSX:
		return newXLSXWriter(w, sheet)
	}
	return nil, fmt.Errorf("imports: %q is not a format the package writes", format)
}

// contentType and extension are what a file of a format is served and named as.
func contentType(format string) string {
	if format == FormatXLSX {
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "text/csv; charset=utf-8"
}

func extension(format string) string {
	if format == FormatXLSX {
		return ".xlsx"
	}
	return ".csv"
}

// csvWriter writes what a spreadsheet in Brazil opens without asking: UTF-8
// with a byte-order mark, ";" between the cells and CRLF between the rows.
type csvWriter struct {
	w       *csv.Writer
	started bool
	raw     io.Writer
}

func newCSVWriter(w io.Writer) *csvWriter {
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.UseCRLF = true
	return &csvWriter{w: cw, raw: w}
}

func (c *csvWriter) begin() error {
	if c.started {
		return nil
	}
	c.started = true
	_, err := c.raw.Write([]byte{0xEF, 0xBB, 0xBF})
	return err
}

func (c *csvWriter) write(cells []string, safe bool) error {
	if err := c.begin(); err != nil {
		return err
	}
	out := make([]string, len(cells))
	for i, v := range cells {
		if safe {
			v = neutralize(v)
		}
		out[i] = v
	}
	return c.w.Write(out)
}

func (c *csvWriter) Header(cells []string) error { return c.write(cells, false) }
func (c *csvWriter) Row(cells []string) error    { return c.write(cells, true) }
func (c *csvWriter) Close() error {
	if err := c.begin(); err != nil {
		return err
	}
	c.w.Flush()
	return c.w.Error()
}

// neutralize keeps a cell a spreadsheet would read as a formula from being run
// when the file is opened. A value that begins with =, +, - or @ (or a tab or a
// carriage return, which some versions skip over) is written with a leading
// apostrophe, which the spreadsheet does not show. The content is not lost,
// which a note somebody typed has to be able to say.
func neutralize(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// xlsxWriter writes a workbook of one sheet with the standard library alone,
// row by row, so an export of tens of thousands of rows is never held in
// memory as one string. Every cell is text: a plate, a document number or a
// phone keeps what was written, with its leading zeros, and a text cell is
// never a formula.
type xlsxWriter struct {
	z       *zip.Writer
	sheet   io.Writer
	row     int
	widths  []int
	pending bytes.Buffer
}

// The style indexes of styles.xml below: 0 plain, 1 bold.
const styleBold = 1

func newXLSXWriter(w io.Writer, sheet string) (*xlsxWriter, error) {
	z := zip.NewWriter(w)
	x := &xlsxWriter{z: z}
	part := func(name, body string) error {
		f, err := z.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, xml.Header+body)
		return err
	}
	steps := []struct{ name, body string }{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`},
		{"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` +
			fmt.Sprintf(`<sheet name="%s" sheetId="1" r:id="rId1"/>`, escapeXML(sheetName(sheet))) + `</sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`},
		{"xl/styles.xml", `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
			`<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>` +
			`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>` +
			`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
			`<cellXfs count="2"><xf numFmtId="49" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>` +
			`<xf numFmtId="49" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1" applyNumberFormat="1"/></cellXfs>` +
			`</styleSheet>`},
	}
	for _, s := range steps {
		if err := part(s.name, s.body); err != nil {
			return nil, err
		}
	}
	return x, nil
}

// begin opens the sheet part. The column widths come first in the XML and are
// known only after the heading row, so the heading is held back until then.
func (x *xlsxWriter) begin() error {
	if x.sheet != nil {
		return nil
	}
	f, err := x.z.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return err
	}
	x.sheet = f
	_, err = io.WriteString(f, xml.Header+`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	return err
}

func (x *xlsxWriter) Header(cells []string) error {
	if x.row != 0 {
		return fmt.Errorf("imports: the heading is written first")
	}
	if err := x.begin(); err != nil {
		return err
	}
	var cols strings.Builder
	cols.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`)
	for i, c := range cells {
		width := utf8.RuneCountInString(c) + 4
		if width < 14 {
			width = 14
		}
		if width > 60 {
			width = 60
		}
		fmt.Fprintf(&cols, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, width)
	}
	cols.WriteString(`</cols><sheetData>`)
	if _, err := io.WriteString(x.sheet, cols.String()); err != nil {
		return err
	}
	return x.write(cells, styleBold)
}

func (x *xlsxWriter) Row(cells []string) error {
	if x.row == 0 {
		if err := x.Header(nil); err != nil {
			return err
		}
	}
	return x.write(cells, 0)
}

func (x *xlsxWriter) write(cells []string, style int) error {
	x.row++
	x.pending.Reset()
	fmt.Fprintf(&x.pending, `<row r="%d">`, x.row)
	for c, v := range cells {
		if v == "" {
			continue
		}
		fmt.Fprintf(&x.pending, `<c r="%s%d" t="inlineStr" s="%d"><is><t xml:space="preserve">%s</t></is></c>`, columnLetters(c), x.row, style, escapeXML(v))
	}
	x.pending.WriteString(`</row>`)
	_, err := x.sheet.Write(x.pending.Bytes())
	return err
}

func (x *xlsxWriter) Close() error {
	if x.row == 0 {
		if err := x.Header(nil); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(x.sheet, `</sheetData></worksheet>`); err != nil {
		return err
	}
	return x.z.Close()
}

// columnLetters is the letters of a zero-based column index: 0 is A, 26 is AA.
func columnLetters(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func escapeXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sheetName is a name Excel accepts: at most 31 characters, none of []:*?/\.
func sheetName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, s)
	if strings.TrimSpace(s) == "" {
		s = "Sheet1"
	}
	if len([]rune(s)) > 31 {
		s = string([]rune(s)[:31])
	}
	return s
}

// itoa is strconv.Itoa for the places that read better with a short name.
func itoa(n int) string { return strconv.Itoa(n) }
