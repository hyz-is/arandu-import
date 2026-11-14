package imports

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

// These tests need what is not exported: the readers and the writers. They are
// the ones that hold the formats.

func cells(g grid) [][]string {
	out := make([][]string, len(g.rows))
	for i, r := range g.rows {
		out[i] = r.cells
	}
	return out
}

func equal(a, b [][]string) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func TestACSVIsReadWithEachDelimiter(t *testing.T) {
	want := [][]string{{"Placa", "Nota"}, {"ABC1D23", "Carro, azul"}, {"QWE2R45"}}
	cases := map[string]string{
		"semicolon": "Placa;Nota\nABC1D23;\"Carro, azul\"\nQWE2R45;\n",
		"comma":     "Placa,Nota\nABC1D23,\"Carro, azul\"\nQWE2R45,\n",
		"tab":       "Placa\tNota\nABC1D23\tCarro, azul\nQWE2R45\t\n",
		"crlf":      "Placa;Nota\r\nABC1D23;\"Carro, azul\"\r\nQWE2R45;\r\n",
		"blanks":    "Placa;Nota\n\nABC1D23;\"Carro, azul\"\n;;\nQWE2R45;\n\n",
	}
	for name, text := range cases {
		g, err := readGrid("placas.csv", []byte(text), 0)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !equal(cells(g), want) {
			t.Errorf("%s: read %v, want %v", name, cells(g), want)
		}
	}
}

func TestACSVKeepsItsLineNumbersAroundBlankLines(t *testing.T) {
	g, err := readGrid("placas.csv", []byte("Placa;Nota\n\nABC1D23;x\n\n\nQWE2R45;y\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, r := range g.rows {
		numbers = append(numbers, r.number)
	}
	if fmt.Sprint(numbers) != "[1 3 6]" {
		t.Errorf("line numbers = %v, want [1 3 6], as the person's editor numbers them", numbers)
	}
}

func TestACSVIsReadInEachEncoding(t *testing.T) {
	want := [][]string{{"Placa", "Nota"}, {"ABC1D23", "João e a Zélia — carro “azul”"}}
	text := "Placa;Nota\nABC1D23;João e a Zélia — carro “azul”\n"

	utf16le := func() []byte {
		units := utf16.Encode([]rune(text))
		out := []byte{0xFF, 0xFE}
		for _, u := range units {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}()
	utf16be := func() []byte {
		units := utf16.Encode([]rune(text))
		out := []byte{0xFE, 0xFF}
		for _, u := range units {
			out = append(out, byte(u>>8), byte(u))
		}
		return out
	}()
	// Windows-1252: what Excel on Windows saves "CSV" as. The dash and the
	// quotes are in the 0x80-0x9F range, where it differs from Latin-1.
	win := []byte("Placa;Nota\nABC1D23;Jo\xe3o e a Z\xe9lia \x97 carro \x93azul\x94\n")

	cases := map[string][]byte{
		"utf8":    []byte(text),
		"utf8bom": append([]byte{0xEF, 0xBB, 0xBF}, text...),
		"utf16le": utf16le,
		"utf16be": utf16be,
		"cp1252":  win,
	}
	for name, body := range cases {
		g, err := readGrid("placas.csv", body, 0)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !equal(cells(g), want) {
			t.Errorf("%s: read %v, want %v", name, cells(g), want)
		}
	}
}

func TestAFileThatIsNotATableIsRefusedWithAReason(t *testing.T) {
	cases := map[string]struct {
		name string
		body []byte
		code string
	}{
		"empty":      {"a.csv", []byte("  \n"), "file_empty"},
		"pdf":        {"a.pdf", []byte("%PDF-1.4"), "file_format"},
		"binary csv": {"a.csv", []byte("a;b\x00\x01\x02"), "file_format"},
		"old xls":    {"a.xls", []byte("whatever"), "file_xls"},
		"ole":        {"a.csv", append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 20)...), "file_xls"},
		"fake xlsx":  {"a.xlsx", []byte("plain text"), "file_corrupt"},
		"bad zip":    {"a.xlsx", append([]byte("PK\x03\x04"), make([]byte, 20)...), "file_corrupt"},
	}
	for name, c := range cases {
		_, err := readGrid(c.name, c.body, 0)
		var p problem
		if !errors.As(err, &p) || p.code != c.code {
			t.Errorf("%s: err = %v, want the problem %s", name, err, c.code)
		}
	}
}

func TestTheReaderStopsAtItsLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("Placa\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "ABC%04d\n", i)
	}
	g, err := readGrid("a.csv", []byte(b.String()), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.rows) != 10 || !g.truncated {
		t.Errorf("read %d rows, truncated=%v; want 10 and truncated", len(g.rows), g.truncated)
	}
}

// workbook writes an XLSX through the package's own writer.
func workbook(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := newTableWriter(FormatXLSX, &buf, "Placas")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Header(rows[0]); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows[1:] {
		if err := w.Row(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnXLSXWrittenHereIsReadBack(t *testing.T) {
	rows := [][]string{
		{"Placa", "Nota"},
		{"ABC1D23", "João & <Maria> \"quote\""},
		{"0012345", "leading zeros stay"},
		{"QWE2R45", ""},
	}
	g, err := readGrid("placas.xlsx", workbook(t, rows), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{rows[0], rows[1], rows[2], {"QWE2R45"}}
	if !equal(cells(g), want) {
		t.Errorf("read %v, want %v", cells(g), want)
	}
	if g.format != FormatXLSX {
		t.Errorf("format = %s", g.format)
	}
}

// excelWorkbook is a workbook the way Excel writes one: shared strings, a
// number cell, an empty cell between two others, a rich-text string, a second
// sheet that must be ignored, and rows that are not consecutive.
func excelWorkbook(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	put := func(name, body string) {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(body))
	}
	put("[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`)
	put("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`+
		`<sheet name="Placas" sheetId="1" r:id="rId2"/><sheet name="Outra" sheetId="2" r:id="rId1"/></sheets></workbook>`)
	put("xl/_rels/workbook.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="x" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="x" Target="worksheets/sheet2.xml"/></Relationships>`)
	put("xl/sharedStrings.xml", `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+
		`<si><t>Placas</t></si><si><t>Notas</t></si><si><t>ABC1D23</t></si>`+
		`<si><r><t>Carro </t></r><r><t>azul</t></r><rPh><t>fonetico</t></rPh></si></sst>`)
	put("xl/worksheets/sheet1.xml", `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>other sheet</t></is></c></row></sheetData></worksheet>`)
	put("xl/worksheets/sheet2.xml", `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>`+
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1.2345E+07</v></c><c r="C2" t="s"><v>3</v></c></row>`+
		`<row r="5"><c r="A5"><v>12</v></c><c r="B5"><v>0.5</v></c><c r="C5" t="b"><v>1</v></c></row>`+
		`<row r="7"><c r="A7"/></row>`+
		`</sheetData></worksheet>`)
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAnXLSXFromExcelIsReadByItsFirstSheet(t *testing.T) {
	g, err := readGrid("placas.xlsx", excelWorkbook(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"Placas", "", "Notas"},
		{"ABC1D23", "12345000", "Carro azul"},
		{"12", "0.5", "TRUE"},
	}
	if !equal(cells(g), want) {
		t.Errorf("read %v, want %v", cells(g), want)
	}
	var numbers []int
	for _, r := range g.rows {
		numbers = append(numbers, r.number)
	}
	if fmt.Sprint(numbers) != "[1 2 5]" {
		t.Errorf("row numbers = %v, want the spreadsheet's own [1 2 5]", numbers)
	}
}

func TestAZipThatInflatesIntoGigabytesIsRefused(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, _ := z.Create("xl/workbook.xml")
	_, _ = f.Write([]byte("<workbook/>"))
	big, _ := z.Create("xl/worksheets/sheet1.xml")
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for i := 0; i < 140; i++ {
		_, _ = big.Write(chunk)
	}
	_ = z.Close()
	_, err := readGrid("bomb.xlsx", buf.Bytes(), 0)
	var p problem
	if !errors.As(err, &p) || p.code != "file_corrupt" {
		t.Errorf("a zip bomb was read: %v", err)
	}
}

func TestACSVWrittenHereOpensInASpreadsheetInBrazil(t *testing.T) {
	var buf bytes.Buffer
	w, _ := newTableWriter(FormatCSV, &buf, "x")
	_ = w.Header([]string{"Placa", "Nota"})
	_ = w.Row([]string{"ABC1D23", "=HYPERLINK(\"http://x\")"})
	_ = w.Row([]string{"QWE2R45", "João; \"azul\""})
	_ = w.Close()
	got := buf.String()
	if !strings.HasPrefix(got, "\xef\xbb\xbfPlaca;Nota\r\n") {
		t.Errorf("no byte-order mark, semicolon or CRLF: %q", got)
	}
	if !strings.Contains(got, "'=HYPERLINK") {
		t.Errorf("a cell that is a formula was written as one: %q", got)
	}
	g, err := readGrid("x.csv", buf.Bytes(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if g.rows[2].cells[1] != "João; \"azul\"" {
		t.Errorf("a cell with the delimiter and quotes did not survive: %q", g.rows[2].cells[1])
	}
}

func TestHeadingsAreFoundWhateverTheirAccentsAndCase(t *testing.T) {
	cols := []Column{
		{Key: "plate", Label: "Placa", Required: true, Headers: []string{"Placas"}},
		{Key: "note", Label: "Nota", Headers: []string{"Observação"}},
		{Key: "email", Label: "E-mail"},
	}
	m, missing := mapColumns(cols, []string{"  OBSERVAÇÃO ", "placas", "Cor", "e mail"})
	if len(missing) != 0 || !m.header {
		t.Fatalf("header=%v missing=%v", m.header, missing)
	}
	if m.index["note"] != 0 || m.index["plate"] != 1 || m.index["email"] != 3 {
		t.Errorf("index = %v", m.index)
	}
	if fmt.Sprint(m.ignored) != "[Cor]" {
		t.Errorf("ignored = %v", m.ignored)
	}
	row := m.row(cols, []string{"obs", "ABC1D23", "azul", "a@b.c"})
	if row["plate"] != "ABC1D23" || row["note"] != "obs" || row["email"] != "a@b.c" {
		t.Errorf("row = %v", row)
	}

	_, missing = mapColumns(cols, []string{"Nota", "Cor"})
	if fmt.Sprint(missing) != "[Placa]" {
		t.Errorf("missing = %v, want the required column named", missing)
	}

	// No heading: the first row is data, read in the order of the columns.
	m, missing = mapColumns(cols, []string{"ABC1D23", "carro"})
	if m.header || len(missing) != 0 || m.index["plate"] != 0 || m.index["note"] != 1 {
		t.Errorf("headerless: header=%v index=%v missing=%v", m.header, m.index, missing)
	}
}
