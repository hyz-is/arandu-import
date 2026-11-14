package imports

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
)

// A workbook is a zip of XML parts. This reads the cells of its first sheet with
// the standard library alone, so the package adds no spreadsheet dependency and
// the binary of whoever installs it stays static.
//
// A workbook comes from outside, so every part is read through a limit and the
// declared sizes are added up first: a few kilobytes of zip that inflate into
// gigabytes is the oldest trick there is.

const (
	// maxPartBytes is the most one part of the workbook may inflate to.
	maxPartBytes = 64 << 20
	// maxWorkbookBytes is the most all the parts together may inflate to.
	maxWorkbookBytes = 128 << 20
	// maxColumns is how far to the right a cell is read. A reference past it is
	// somebody's stray cell, and building a row that wide would cost memory for
	// nothing.
	maxColumns = 200
)

// readXLSX reads the first sheet of the workbook in body.
func readXLSX(body []byte, limit int) (grid, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return grid{}, problemOf("file_corrupt")
	}
	parts := map[string]*zip.File{}
	var declared uint64
	for _, f := range zr.File {
		parts[f.Name] = f
		declared += f.UncompressedSize64
	}
	if declared > maxWorkbookBytes {
		return grid{}, problemOf("file_corrupt")
	}
	if _, ok := parts["xl/workbook.xml"]; !ok {
		return grid{}, problemOf("file_corrupt")
	}

	target, err := firstSheet(parts)
	if err != nil {
		return grid{}, err
	}
	strs, err := sharedStrings(parts)
	if err != nil {
		return grid{}, err
	}
	sheet, ok := parts[target]
	if !ok {
		return grid{}, problemOf("file_corrupt")
	}
	rc, err := sheet.Open()
	if err != nil {
		return grid{}, problemOf("file_corrupt")
	}
	defer rc.Close()
	g, err := readSheet(io.LimitReader(rc, maxPartBytes), strs, limit)
	if err != nil {
		return grid{}, err
	}
	if len(g.rows) == 0 {
		return grid{}, problemOf("file_empty")
	}
	return g, nil
}

func readPart(parts map[string]*zip.File, name string) ([]byte, bool, error) {
	f, ok := parts[name]
	if !ok {
		return nil, false, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, true, problemOf("file_corrupt")
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
	if err != nil || len(b) > maxPartBytes {
		return nil, true, problemOf("file_corrupt")
	}
	return b, true, nil
}

// firstSheet names the part holding the first sheet, by the order the workbook
// lists its sheets in and the relationships that say where each one is.
func firstSheet(parts map[string]*zip.File) (string, error) {
	book, _, err := readPart(parts, "xl/workbook.xml")
	if err != nil {
		return "", err
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(book, &wb); err != nil || len(wb.Sheets) == 0 {
		return "", problemOf("file_corrupt")
	}
	rels, ok, err := readPart(parts, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	if ok {
		var rl struct {
			Items []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(rels, &rl); err == nil {
			for _, item := range rl.Items {
				if item.ID != wb.Sheets[0].ID {
					continue
				}
				target := item.Target
				if strings.HasPrefix(target, "/") {
					return strings.TrimPrefix(target, "/"), nil
				}
				return path.Join("xl", target), nil
			}
		}
	}
	return "xl/worksheets/sheet1.xml", nil
}

// sharedStrings reads the table of strings the cells refer to by position.
func sharedStrings(parts map[string]*zip.File) ([]string, error) {
	b, ok, err := readPart(parts, "xl/sharedStrings.xml")
	if err != nil || !ok {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(b))
	var out []string
	var cur strings.Builder
	inItem, inText, inPhonetic := false, false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, problemOf("file_corrupt")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inItem = true
				cur.Reset()
			case "t":
				inText = inItem && !inPhonetic
			case "rPh":
				inPhonetic = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inItem = false
				out = append(out, cur.String())
			case "t":
				inText = false
			case "rPh":
				inPhonetic = false
			}
		case xml.CharData:
			if inText {
				cur.Write(t)
			}
		}
	}
	return out, nil
}

// readSheet streams the rows of a sheet.
func readSheet(r io.Reader, strs []string, limit int) (grid, error) {
	dec := xml.NewDecoder(r)
	g := grid{format: FormatXLSX}
	var (
		rowNumber int
		cells     []string
		col       int
		cellType  string
		value     strings.Builder
		inline    strings.Builder
		inRow     bool
		inCell    bool
		inV       bool
		inT       bool
		inInline  bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return grid{}, problemOf("file_corrupt")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow = true
				cells = cells[:0]
				rowNumber++
				for _, a := range t.Attr {
					if a.Name.Local == "r" {
						if n, err := strconv.Atoi(a.Value); err == nil && n > 0 {
							rowNumber = n
						}
					}
				}
				col = 0
			case "c":
				if !inRow {
					continue
				}
				inCell = true
				cellType = ""
				value.Reset()
				inline.Reset()
				next := col
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						if idx, ok := columnIndex(a.Value); ok {
							next = idx
						}
					case "t":
						cellType = a.Value
					}
				}
				col = next
			case "v":
				inV = inCell
			case "is":
				inInline = inCell
			case "t":
				inT = inInline
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "is":
				inInline = false
			case "c":
				if inCell {
					text := cellText(cellType, value.String(), inline.String(), strs)
					if col < maxColumns {
						for len(cells) <= col {
							cells = append(cells, "")
						}
						cells[col] = cleanCell(text)
					}
					col++
				}
				inCell = false
			case "row":
				inRow = false
				cs := trimTrailing(append([]string(nil), cells...))
				blank := true
				for _, c := range cs {
					if c != "" {
						blank = false
						break
					}
				}
				if blank {
					continue
				}
				if limit > 0 && len(g.rows) >= limit {
					g.truncated = true
					return g, nil
				}
				g.rows = append(g.rows, gridRow{number: rowNumber, cells: cs})
			}
		case xml.CharData:
			switch {
			case inV:
				value.Write(t)
			case inT:
				inline.Write(t)
			}
		}
	}
	return g, nil
}

// cellText is the text a cell shows: the string a shared-string cell points
// at, an inline string, a boolean, or a number written the way a person would
// type it.
func cellText(kind, v, inline string, strs []string) string {
	switch kind {
	case "s":
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && i >= 0 && i < len(strs) {
			return strs[i]
		}
		return ""
	case "inlineStr":
		return inline
	case "b":
		if strings.TrimSpace(v) == "1" {
			return "TRUE"
		}
		return "FALSE"
	case "e":
		return ""
	case "str", "d":
		return v
	}
	return numberText(strings.TrimSpace(v))
}

// numberText writes a number a cell holds without the exponent a spreadsheet
// stores it with: 1.2345E+07 is 12345000, and a whole number has no ".0".
func numberText(v string) string {
	if v == "" {
		return ""
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return v
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// columnIndex reads the column of a reference such as "C7": C is 2.
func columnIndex(ref string) (int, bool) {
	n := 0
	seen := false
	for _, r := range ref {
		switch {
		case r >= 'A' && r <= 'Z':
			n = n*26 + int(r-'A') + 1
			seen = true
		case r >= 'a' && r <= 'z':
			n = n*26 + int(r-'a') + 1
			seen = true
		default:
			if !seen {
				return 0, false
			}
			return n - 1, true
		}
		if n > 1<<20 {
			return 0, false
		}
	}
	if !seen {
		return 0, false
	}
	return n - 1, true
}
