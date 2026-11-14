package imports

import (
	"strings"
	"unicode"
)

// plain lowers a heading and takes out what people disagree on when they type
// one: the accents, the spaces, the hyphens and every other mark. "E-mail",
// "email" and " E MAIL " are one heading, and so are "Série" and "serie".
func plain(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if base, ok := unaccented[r]; ok {
			r = base
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var unaccented = func() map[rune]rune {
	m := map[rune]rune{}
	for base, set := range map[rune]string{
		'a': "áàâãäåā", 'c': "çć", 'e': "éèêëē", 'i': "íìîïī", 'n': "ñ",
		'o': "óòôõöø", 'u': "úùûüū", 'y': "ýÿ",
	} {
		for _, r := range set {
			m[r] = base
		}
	}
	return m
}()

// mapping says which cell of a row is which column.
type mapping struct {
	// header says the first row was a heading and is not data.
	header bool
	// index is the cell each column is read from, by the column's key.
	index map[string]int
	// columns are the definition's columns with the heading each was found
	// under, for the preview.
	columns []MappedColumn
	// ignored are the headings no column answers to.
	ignored []string
}

// mapColumns decides which cell is which column. The first row is a heading
// when at least one of its cells is a name the definition knows; then each
// column is the cell with its name, in whatever order, and a column the file
// does not have is empty. A first row that names nothing is data, and the cells
// are the columns in the order the definition lists them.
//
// It returns the keys of the required columns the file lacks, in the order the
// definition lists them, when a heading is there and one is missing.
func mapColumns(cols []Column, first []string) (mapping, []string) {
	names := make([]map[string]bool, len(cols))
	for i, c := range cols {
		names[i] = map[string]bool{plain(c.Key): true, plain(c.Label): true}
		for _, h := range c.Headers {
			names[i][plain(h)] = true
		}
	}

	m := mapping{index: map[string]int{}}
	taken := map[int]bool{}
	for i, c := range cols {
		for cell, text := range first {
			if taken[cell] || text == "" || !names[i][plain(text)] {
				continue
			}
			m.index[c.Key] = cell
			taken[cell] = true
			m.columns = append(m.columns, MappedColumn{Key: c.Key, Label: c.Label, Heading: text, Required: c.Required})
			break
		}
	}

	if len(m.index) > 0 {
		m.header = true
		var missing []string
		for _, c := range cols {
			if _, found := m.index[c.Key]; !found {
				m.columns = append(m.columns, MappedColumn{Key: c.Key, Label: c.Label, Required: c.Required})
				if c.Required {
					missing = append(missing, c.Label)
				}
			}
		}
		m.columns = orderLike(cols, m.columns)
		for cell, text := range first {
			if !taken[cell] && text != "" {
				m.ignored = append(m.ignored, text)
			}
		}
		return m, missing
	}

	// No heading: the cells are the columns, in order.
	m.columns = m.columns[:0]
	var missing []string
	for i, c := range cols {
		if i < len(first) {
			m.index[c.Key] = i
			m.columns = append(m.columns, MappedColumn{Key: c.Key, Label: c.Label, Required: c.Required})
			continue
		}
		m.columns = append(m.columns, MappedColumn{Key: c.Key, Label: c.Label, Required: c.Required})
		if c.Required {
			missing = append(missing, c.Label)
		}
	}
	for i := len(cols); i < len(first); i++ {
		if first[i] != "" {
			m.ignored = append(m.ignored, first[i])
		}
	}
	return m, missing
}

// orderLike puts the mapped columns in the order the definition lists them.
func orderLike(cols []Column, mapped []MappedColumn) []MappedColumn {
	pos := map[string]int{}
	for i, c := range cols {
		pos[c.Key] = i
	}
	out := make([]MappedColumn, len(cols))
	for _, m := range mapped {
		out[pos[m.Key]] = m
	}
	return out
}

// row reads the cells of one row as the definition's columns.
func (m mapping) row(cols []Column, cells []string) Row {
	r := make(Row, len(cols))
	for _, c := range cols {
		if i, ok := m.index[c.Key]; ok && i < len(cells) {
			r[c.Key] = cells[i]
		} else {
			r[c.Key] = ""
		}
	}
	return r
}

// data is the rows of the grid that are data: all of them, or all but the
// first when that one is a heading.
func (m mapping) data(rows []gridRow) []gridRow {
	if m.header && len(rows) > 0 {
		return rows[1:]
	}
	return rows
}
