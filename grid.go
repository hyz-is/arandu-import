package imports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// grid is the cells of a file, row by row, whatever the format it came in.
type grid struct {
	// format is FormatCSV or FormatXLSX.
	format string
	rows   []gridRow
	// truncated says the file had more rows than the reader was asked for. The
	// reader stops there, so a file of a million rows costs what the limit does.
	truncated bool
}

// gridRow is one row: its cells and its place in the file, from 1, as the
// person's spreadsheet numbers it. A row that is blank is not here, and the
// numbers around it say it was.
type gridRow struct {
	number int
	cells  []string
}

// problem is why a file could not be read, as a code the service words in the
// person's language.
type problem struct {
	code string
	args map[string]any
}

func (p problem) Error() string { return fmt.Sprintf("imports: the file cannot be read: %s", p.code) }

func problemOf(code string, kv ...any) problem {
	p := problem{code: code}
	if len(kv) > 0 {
		p.args = map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			p.args[fmt.Sprint(kv[i])] = kv[i+1]
		}
	}
	return p
}

// The extensions a file may have. The content is checked as well: the extension
// is what the person called it, and a request that bypassed the page carries
// whatever it likes.
var extensions = map[string]string{
	".csv": FormatCSV, ".txt": FormatCSV, ".tsv": FormatCSV, ".xlsx": FormatXLSX,
}

// AcceptedExtensions are the extensions the screens offer to the file picker.
func AcceptedExtensions() []string { return []string{".csv", ".xlsx", ".txt", ".tsv"} }

var (
	zipMagic = []byte("PK\x03\x04")
	oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// readGrid reads the file into cells. limit is how many rows to read before
// stopping; zero reads them all.
func readGrid(name string, body []byte, limit int) (grid, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return grid{}, problemOf("file_empty")
	}
	ext := strings.ToLower(path.Ext(strings.ReplaceAll(name, `\`, "/")))
	want, known := extensions[ext]
	if !known {
		if ext == ".xls" || bytes.HasPrefix(body, oleMagic) {
			return grid{}, problemOf("file_xls")
		}
		return grid{}, problemOf("file_format")
	}
	switch {
	case bytes.HasPrefix(body, oleMagic):
		return grid{}, problemOf("file_xls")
	case bytes.HasPrefix(body, zipMagic):
		return readXLSX(body, limit)
	case want == FormatXLSX:
		// It says it is a workbook and it is not a zip.
		return grid{}, problemOf("file_corrupt")
	}
	return readCSV(body, limit)
}

// readCSV reads a delimited text: UTF-8, UTF-16 or Latin-1/Windows-1252, with
// ";", "," or a tab, with a heading or without.
func readCSV(body []byte, limit int) (grid, error) {
	if looksBinary(body) {
		return grid{}, problemOf("file_format")
	}
	text := decodeText(body)
	comma := sniffDelimiter(text)
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = comma
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	g := grid{format: FormatCSV}
	for {
		record, err := r.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// A quote the reader cannot make sense of, even leniently: say so
			// rather than guess at what the rest of the file was.
			return grid{}, problemOf("file_corrupt")
		}
		line, _ := r.FieldPos(0)
		cells := make([]string, len(record))
		blank := true
		for i, c := range record {
			cells[i] = cleanCell(c)
			if cells[i] != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		if limit > 0 && len(g.rows) >= limit {
			g.truncated = true
			break
		}
		g.rows = append(g.rows, gridRow{number: line, cells: trimTrailing(cells)})
	}
	if len(g.rows) == 0 {
		return grid{}, problemOf("file_empty")
	}
	return g, nil
}

// looksBinary says the content has bytes no delimited text has. A UTF-16 file
// is full of NULs, and is let through by its byte-order mark.
func looksBinary(b []byte) bool {
	if bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || bytes.HasPrefix(b, []byte{0xFE, 0xFF}) {
		return false
	}
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// cleanCell trims a cell of the spaces a spreadsheet leaves around a value,
// including the non-breaking and the zero-width ones a paste brings.
func cleanCell(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\u200b' || r == '\ufeff' || r == '\u00a0'
	})
}

func trimTrailing(cells []string) []string {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// decodeText turns the bytes of a text file into a string. A byte-order mark
// says what it is; without one, a file that is valid UTF-8 is UTF-8, and
// anything else is what Excel on Windows writes: Windows-1252.
func decodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeUTF16(b[2:], false)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeUTF16(b[2:], true)
	case utf8.Valid(b):
		return string(b)
	}
	return decodeWindows1252(b)
}

func decodeUTF16(b []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(units))
}

// windows1252 maps the bytes 0x80 to 0x9F, where Windows-1252 differs from
// Latin-1. Everything else of the two is the same code point as the byte.
var windows1252 = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

func decodeWindows1252(b []byte) string {
	var out strings.Builder
	out.Grow(len(b) + len(b)/4)
	for _, c := range b {
		switch {
		case c >= 0x80 && c <= 0x9F:
			out.WriteRune(windows1252[c-0x80])
		default:
			out.WriteRune(rune(c))
		}
	}
	return out.String()
}

// sniffDelimiter picks the separator of a text: the one that appears, outside
// quotes, in the most of its first lines, with ties going to the one that
// appears most and then to ";", the separator of a Brazilian spreadsheet.
func sniffDelimiter(text string) rune {
	candidates := []rune{';', '\t', ','}
	lines := 0
	present := map[rune]int{}
	total := map[rune]int{}
	for _, line := range strings.Split(text, "\n") {
		if lines >= 10 {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		inQuote := false
		counts := map[rune]int{}
		for _, r := range line {
			switch {
			case r == '"':
				inQuote = !inQuote
			case !inQuote:
				counts[r]++
			}
		}
		for _, d := range candidates {
			if counts[d] > 0 {
				present[d]++
				total[d] += counts[d]
			}
		}
	}
	best := ';'
	for _, d := range candidates {
		switch {
		case present[d] > present[best]:
			best = d
		case present[d] == present[best] && present[d] > 0 && total[d] > total[best]:
			best = d
		}
	}
	return best
}
