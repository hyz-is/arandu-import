package imports

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/arandu-io/framework/security"
)

// Row is one line of a file as the definition sees it: the value of each
// column by the column's Key, trimmed. A column the file does not carry, or
// leaves empty, is the empty string.
type Row map[string]string

// Column is one field of a definition: what the file heads it with, what the
// server calls it, and whether a row without it is refused.
type Column struct {
	// Key is the name the application reads the value by: lowercase letters,
	// digits and underscores.
	Key string
	// Label is the heading the template, the report and the export write.
	Label string
	// Headers are the other headings a file may use for the column. The Key and
	// the Label are always accepted, whatever their accents, case and spaces.
	Headers []string
	// Required says a file without the column is refused as a whole. An empty
	// cell of a required column is for Validate to refuse, since only the
	// application knows what an empty value means.
	Required bool
	// Hint is a line of help shown beside the column on the import screen.
	Hint string
	// Example is the value the template shows when the definition has no
	// Examples of its own.
	Example string
}

// Outcome is what writing a row did.
type Outcome int

const (
	// Created says the row made a new record.
	Created Outcome = iota + 1
	// Updated says the row changed a record that existed.
	Updated
	// Unchanged says the record already held what the row says.
	Unchanged
)

// Refusal is the error that refuses one row and nothing else: its Reason is
// what the report tells the person, beside the row's number. Any other error a
// Definition returns is not about the row, and stops the import.
type Refusal struct{ Reason string }

// Error is the reason.
func (r *Refusal) Error() string { return r.Reason }

// Refuse refuses the row it is returned for.
func Refuse(reason string) error { return &Refusal{Reason: reason} }

// reasonOf says whether err refuses a row, and why.
func reasonOf(err error) (string, bool) {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Reason, true
	}
	return "", false
}

// Summary is what a finished import says, as a definition's Finish is told.
type Summary struct {
	// Definition is the key of the definition.
	Definition string
	// Total is the rows read, and Created, Updated, Unchanged and Refused what
	// became of them; the four add up to Total.
	Total, Created, Updated, Unchanged, Refused int
}

// Export is how a definition writes its records out: the columns of the file
// and the application's own query, called with the Grant of whoever asked.
type Export struct {
	// Columns are the columns of the file. Empty means the definition's own.
	Columns []Column
	// Each calls emit once for every record, in the order the file lists them.
	// The Grant carries the tenant: the query reads nothing from anywhere else.
	// Each stops at the first error emit returns, and returns it.
	Each func(ctx context.Context, g security.Grant, emit func(Row) error) error
	// MaxRows is how many rows one export holds. Zero means DefaultExportRows,
	// and a file that would hold more is refused rather than cut short.
	MaxRows int
}

// Definition is one kind of import, and optionally of export: the application
// states it once and the package draws the screens, reads the file, checks and
// writes the rows in the background and reports what happened.
type Definition struct {
	// Key names the definition in the address: lowercase letters, digits and
	// hyphens. It is stored on every run, so changing it orphans the history.
	Key string
	// Label is its name on the screens: "Lista de permitidos".
	Label string
	// Description is the lead of the import screen.
	Description string
	// Columns are the fields of a row, in the order the template writes them.
	Columns []Column
	// MaxRows is how many rows one file may hold. Zero means DefaultMaxRows,
	// and a file with more is refused whole rather than cut short.
	MaxRows int

	// ImportAction is the action a subject needs to import, and ExportAction
	// the one it needs to export and to see an export. Both are the
	// application's own actions, given to subjects by its permission module.
	ImportAction security.Action
	ExportAction security.Action

	// Validate checks one row and returns it normalized. It is called on the
	// whole file to draw the preview, and again by the job: it writes nothing.
	// An error made with Refuse refuses the row; the Grant is the one of whoever
	// is importing.
	Validate func(ctx context.Context, g security.Grant, row Row) (Row, error)

	// Write stores one validated row with the Grant of whoever confirmed the
	// import, and says what it did. The queue delivers a job at least once, so
	// Write has to be safe to run twice on the same row: an upsert.
	Write func(ctx context.Context, g security.Grant, row Row) (Outcome, error)

	// Finish runs once when an import ends, with the counts. The application
	// uses it for what depends on the whole file, such as telling a live table
	// that it changed. An error is logged and does not undo the import.
	Finish func(ctx context.Context, g security.Grant, s Summary) error

	// Examples are the rows the template shows. Empty uses each column's
	// Example.
	Examples []Row

	// Export, when set, offers the definition's records as a file.
	Export *Export
}

// CanImport says the definition accepts files.
func (d Definition) CanImport() bool { return d.Write != nil }

// CanExport says the definition writes files.
func (d Definition) CanExport() bool { return d.Export != nil && d.Export.Each != nil }

func (d Definition) withDefaults() Definition {
	if d.MaxRows == 0 {
		d.MaxRows = DefaultMaxRows
	}
	if d.Validate == nil {
		d.Validate = func(_ context.Context, _ security.Grant, row Row) (Row, error) { return row, nil }
	}
	if d.Export != nil {
		e := *d.Export
		if e.MaxRows == 0 {
			e.MaxRows = DefaultExportRows
		}
		if len(e.Columns) == 0 {
			e.Columns = d.Columns
		}
		d.Export = &e
	}
	return d
}

var (
	keyPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	columnPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// validateDefinitions refuses what would draw a screen that cannot work.
func validateDefinitions(defs []Definition) error {
	seen := map[string]bool{}
	for i, d := range defs {
		where := fmt.Sprintf("Config.Definitions[%d]", i)
		switch {
		case !keyPattern.MatchString(d.Key):
			return fmt.Errorf("imports: %s.Key is %q: lowercase letters, digits and hyphens, starting with a letter or digit", where, d.Key)
		case seen[d.Key]:
			return fmt.Errorf("imports: %s.Key %q is registered twice", where, d.Key)
		case strings.TrimSpace(d.Label) == "":
			return fmt.Errorf("imports: %s (%s) has no Label", where, d.Key)
		case !d.CanImport() && !d.CanExport():
			return fmt.Errorf("imports: %s (%s) neither imports (Write) nor exports (Export.Each): it would draw an empty screen", where, d.Key)
		case d.CanImport() && d.ImportAction == "":
			return fmt.Errorf("imports: %s (%s) has a Write and no ImportAction: nobody would be allowed to run it, or everybody", where, d.Key)
		case d.CanExport() && d.ExportAction == "":
			return fmt.Errorf("imports: %s (%s) has an Export and no ExportAction", where, d.Key)
		case d.MaxRows < 0 || d.MaxRows > MaxRowsLimit:
			return fmt.Errorf("imports: %s (%s) MaxRows is %d, and has to be between 0 and %d, where 0 means %d", where, d.Key, d.MaxRows, MaxRowsLimit, DefaultMaxRows)
		case d.Export != nil && (d.Export.MaxRows < 0 || d.Export.MaxRows > MaxRowsLimit):
			return fmt.Errorf("imports: %s (%s) Export.MaxRows is %d, and has to be between 0 and %d", where, d.Key, d.Export.MaxRows, MaxRowsLimit)
		case d.Export != nil && d.Export.Each == nil:
			return fmt.Errorf("imports: %s (%s) has an Export with no Each", where, d.Key)
		}
		seen[d.Key] = true
		if d.CanImport() {
			if err := validateColumns(where, d.Key, d.Columns, true); err != nil {
				return err
			}
		}
		if d.Export != nil && len(d.Export.Columns) > 0 {
			if err := validateColumns(where+".Export", d.Key, d.Export.Columns, false); err != nil {
				return err
			}
		}
		if d.CanExport() && len(d.Export.Columns) == 0 && len(d.Columns) == 0 {
			return fmt.Errorf("imports: %s (%s) exports with no columns", where, d.Key)
		}
	}
	return nil
}

func validateColumns(where, key string, cols []Column, needRequired bool) error {
	if len(cols) == 0 {
		return fmt.Errorf("imports: %s (%s) has no Columns", where, key)
	}
	seen := map[string]bool{}
	required := false
	for _, c := range cols {
		switch {
		case !columnPattern.MatchString(c.Key):
			return fmt.Errorf("imports: %s (%s) has a column whose Key is %q: lowercase letters, digits and underscores", where, key, c.Key)
		case seen[c.Key]:
			return fmt.Errorf("imports: %s (%s) has the column %q twice", where, key, c.Key)
		case strings.TrimSpace(c.Label) == "":
			return fmt.Errorf("imports: %s (%s) column %q has no Label", where, key, c.Key)
		}
		seen[c.Key] = true
		required = required || c.Required
	}
	if needRequired && !required {
		return fmt.Errorf("imports: %s (%s) has no required column: a file with nothing in common with the definition would be read as a header-less file of empty rows", where, key)
	}
	return nil
}

// definition returns the definition a key names.
func (c Config) definition(key string) (Definition, bool) {
	for _, d := range c.Definitions {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}
