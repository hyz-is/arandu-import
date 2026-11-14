package imports

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	fview "github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/pagination"
	"github.com/arandu-io/hesape/translation"
	hview "github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
)

// The names the package renders its views by. They come from the paths of the
// files in resources/views, by the rule views.go states.
const (
	ViewIndex = "modules.imports.index"
	ViewShow  = "modules.imports.show"
	ViewState = "modules.imports.state"
)

// URLs builds the addresses of the package from the configuration, so a link to
// an import is written once, here, and never spelled out a second time.
type URLs struct{ cfg Config }

// NewURLs builds the addresses for a prefix and paths without a module: a view
// test, or a menu built before the module is.
func NewURLs(prefix string, paths Paths) URLs {
	return URLs{cfg: Config{Prefix: prefix, Paths: paths}.withDefaultsForURLs()}
}

func (c Config) withDefaultsForURLs() Config {
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	c.Paths = c.Paths.withDefaults()
	return c
}

// Index is the page that lists every definition and the history of the space.
func (u URLs) Index() string { return u.cfg.Prefix }

// Definition is the page of one definition: the upload, the template, the
// export and its history. The upload form posts here.
func (u URLs) Definition(key string) string { return u.cfg.Prefix + "/" + url.PathEscape(key) }

// Template is the address of the template of a definition in a format.
func (u URLs) Template(key, format string) string {
	return u.Definition(key) + u.cfg.Paths.Template + "/" + url.PathEscape(format)
}

// Export is where a request for an export is posted.
func (u URLs) Export(key string) string { return u.Definition(key) + u.cfg.Paths.Export }

// Run is the page of one run.
func (u URLs) Run(key, id string) string { return u.Definition(key) + "/" + url.PathEscape(id) }

// Progress is the fragment of a run's state, which the page asks for again
// while the run is going.
func (u URLs) Progress(key, id string) string { return u.Run(key, id) + u.cfg.Paths.Progress }

// Confirm and Discard are where an upload's preview posts.
func (u URLs) Confirm(key, id string) string { return u.Run(key, id) + u.cfg.Paths.Confirm }
func (u URLs) Discard(key, id string) string { return u.Run(key, id) + u.cfg.Paths.Discard }

// Refused is the CSV of the rows an import turned away, and File the file the
// run read or wrote.
func (u URLs) Refused(key, id string) string { return u.Run(key, id) + u.cfg.Paths.Refused }
func (u URLs) File(key, id string) string    { return u.Run(key, id) + u.cfg.Paths.File }

// Screen is what every view of the package starts from: the page, the labels,
// the addresses and the application's frame.
//
// It embeds the Hesape page, so a layout reads the title, the CSRF token and
// the flash from it as it does from any other page, and it carries what the
// application's Chrome answered, for the layout to draw the sidebar from.
type Screen struct {
	hview.Page
	Labels Labels
	// URLs are the addresses of the package's routes.
	URLs URLs
	// Location is the time zone instants are written in.
	Location *time.Location

	chrome any
}

// Compile-time proof that a page of this package fits both layout contracts.
var (
	_ hview.Layout   = Screen{}
	_ fview.Layout   = Screen{}
	_ slog.LogValuer = Screen{}
)

// Chrome is what Config.Chrome answered for this screen; nil when Config.Chrome
// is nil.
func (s Screen) Chrome() any { return s.chrome }

// HasErrors says whether the attempt that landed here was rejected.
func (s Screen) HasErrors() bool { return s.Page.Any() }

// All is one line per rejected message, in the screen's language.
func (s Screen) All() []string { return s.Labels.Summary(s.Page.Errors) }

// ErrorSummary is All under the name the framework's layout contract uses.
func (s Screen) ErrorSummary() []string { return s.All() }

// FieldErrors are the messages of one field.
func (s Screen) FieldErrors(name string) []string { return s.Page.Get(name) }

// MarshalJSON hands the framework's page a JSON of its own.
func (s Screen) MarshalJSON() ([]byte, error) { return framePage(s.Page).MarshalJSON() }

// LogValue keeps a page out of the logs but for its public fields.
func (s Screen) LogValue() slog.Value { return framePage(s.Page).LogValue() }

// Form is the page as a form's components read it.
func (s Screen) Form() FormState { return FormState{Page: s.Page} }

// FormState is the part of a page a form component asks about.
type FormState struct{ hview.Page }

// FieldError is the first message of a field.
func (f FormState) FieldError(name string) string { return f.Page.FieldError(name) }

// HasErrors says the form was rejected.
func (f FormState) HasErrors() bool { return f.Page.Any() }

// ErrorSummary is every message of the form.
func (f FormState) ErrorSummary() []string { return f.Page.All() }

// FieldErrors are the messages of one field.
func (f FormState) FieldErrors(name string) []string { return f.Page.Get(name) }

// MarshalJSON and LogValue keep the form as quiet as the page.
func (f FormState) MarshalJSON() ([]byte, error) { return framePage(f.Page).MarshalJSON() }
func (f FormState) LogValue() slog.Value         { return framePage(f.Page).LogValue() }

func framePage(p hview.Page) fview.Page {
	return fview.Page{
		Title: p.Title, Description: p.Description, Canonical: p.Canonical, AppName: p.AppName, Token: p.Token,
		Authenticated: p.Authenticated, UserName: p.UserName,
		HomeURL: p.HomeURL, LoginURL: p.LoginURL, LogoutURL: p.LogoutURL, RegisterURL: p.RegisterURL,
		PanelURL: p.PanelURL, AdminURL: p.AdminURL,
		Impersonating: p.Impersonating, ImpersonationActs: p.ImpersonationActs, Impersonator: p.Impersonator,
		LeaveImpersonationURL: p.LeaveImpersonationURL,
		Path:                  p.Path, Errors: p.Errors, Old: p.Old, Messages: p.Messages,
	}
}

// TableLines are the sentences of a data table, in the screen's language.
func (s Screen) TableLines() components.DataTableLines {
	l := s.Labels
	return components.DataTableLines{
		Filter: l.T("table.filter"), NoResult: l.T("table.no_result"), Rows: l.T("table.rows"),
		SelectedCount: l.T("table.selected_count"), RowsPerPage: l.T("table.rows_per_page"), PageOf: l.T("table.page_of"),
		First: l.T("table.first"), Previous: l.T("table.previous"), Next: l.T("table.next"), Last: l.T("table.last"),
		Back: l.T("table.back"), Forward: l.T("table.forward"),
		SelectAll: l.T("table.select_all"), SelectRow: l.T("table.select_row"), Bulk: l.T("table.bulk"), Apply: l.T("table.apply"),
		Ascending: l.T("table.ascending"), Descending: l.T("table.descending"), Unsorted: l.T("table.unsorted"),
		Reset: l.T("table.reset"), ClearFilters: l.T("table.clear_filters"),
		NoFacetResults: l.T("table.no_facet_results"), FacetSelected: l.T("table.facet_selected"),
		OpenMenu: l.T("table.open_menu"), Actions: l.T("table.actions"), ToggleColumns: l.T("table.toggle_columns"),
		Asc: l.T("table.asc"), Desc: l.T("table.desc"), Hide: l.T("table.hide"),
	}
}

// SearchLabel is the accessible name of a table's search box.
func (s Screen) SearchLabel(table string) string {
	return s.Labels.line("control.search_label", map[string]any{"label": table})
}

// StatusOptions are the statuses a history can be filtered by.
func (s Screen) StatusOptions() []components.SelectOption {
	out := make([]components.SelectOption, 0, len(Statuses))
	for _, status := range Statuses {
		out = append(out, components.SelectOption{Value: string(status), Label: s.Labels.Status(status)})
	}
	return out
}

// When writes an instant in the screen's zone.
func (s Screen) When(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	loc := s.Location
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("02/01/2006 15:04")
}

// DefinitionView is a definition as a screen shows it.
type DefinitionView struct {
	Key, Label, Description string
	// URL is the page of the definition.
	URL string
	// CanImport and CanExport say what the subject may do with it.
	CanImport, CanExport bool
}

// ColumnView is a column as the help of the import screen lists it.
type ColumnView struct {
	Label, Hint string
	Required    bool
}

// RunRow is a run as a screen shows it.
type RunRow struct {
	ID, Definition, DefinitionLabel string
	Kind, KindLabel                 string
	Status, StatusKey               string
	StatusVariant                   string
	FileName, Format, Size          string
	Total, Processed                int64
	Created, Updated                int64
	Unchanged, Refused              int64
	Percent                         int
	Result                          string
	When, WhenISO                   string
	Finished                        string
	URL                             string
	Active, IsFinished              bool
	IsImport, IsExport              bool
}

func (s Screen) runRow(r *Run) RunRow {
	row := RunRow{
		ID: r.ID, Definition: r.Definition, DefinitionLabel: r.Definition,
		Kind: r.Kind, KindLabel: s.Labels.Kind(r.Kind),
		Status: s.Labels.Status(r.Current()), StatusKey: r.Status, StatusVariant: StatusVariant(r.Current()),
		FileName: r.FileName, Format: strings.ToUpper(r.Format), Size: size(r.FileSize),
		Total: r.Total, Processed: r.Processed, Created: r.Created, Updated: r.Updated,
		Unchanged: r.Unchanged, Refused: r.Refused, Percent: r.Percent(),
		When: s.When(r.CreatedAt), WhenISO: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		URL:    s.URLs.Run(r.Definition, r.ID),
		Active: r.Current().Active(), IsFinished: r.Finished(),
		IsImport: r.IsImport(), IsExport: r.IsExport(),
	}
	if r.FinishedAt != nil {
		row.Finished = s.When(*r.FinishedAt)
	}
	row.Result = s.result(r)
	return row
}

// result is the one line the history shows of what a run did.
func (s Screen) result(r *Run) string {
	l := s.Labels
	switch r.Current() {
	case StatusUploaded:
		return l.line("result.uploaded", translation.Replace{"total": r.Total})
	case StatusQueued:
		return l.T("result.queued")
	case StatusRunning:
		return l.line("result.running", translation.Replace{"done": r.Processed, "total": r.Total})
	case StatusFailed:
		return l.FailureText(r.Failure)
	case StatusDiscarded:
		return l.T("result.discarded")
	}
	if r.IsExport() {
		return l.line("result.exported", translation.Replace{"rows": r.Total})
	}
	return l.line("result.imported", translation.Replace{
		"created": r.Created, "updated": r.Updated, "unchanged": r.Unchanged, "refused": r.Refused,
	})
}

// IndexPageData is what the index page of the package, and of one definition,
// is drawn from.
type IndexPageData struct {
	Screen
	// Definition is the definition the page is about; nil on the overview.
	Definition *DefinitionView
	// Definitions are the ones the subject may use, on the overview.
	Definitions []DefinitionView

	// The upload: where it posts, what the picker offers, and the limits.
	UploadURL    string
	Accept       string
	MaxMB        string
	MaxRows      int
	Columns      []ColumnView
	TemplateCSV  string
	TemplateXLSX string
	ExportURL    string
	CanImport    bool
	CanExport    bool
	Retention    int

	// The history.
	Rows          []RunRow
	Query         string
	Statuses      []string
	SortKey       string
	SortDir       string
	URL           string
	Pager         components.Paginator
	CurrentPage   int
	Pages, Total  int
	PerPage       int
	StatusOptions []components.SelectOption
}

// TableState is where the history's table is, for its controls.
func (d IndexPageData) TableState() components.DataTableState {
	return components.DataTableValues{
		Q: d.Query, Sort: orderKey(d.SortKey), Dir: orderDir(d.SortDir),
		Facets:      map[string][]string{"status": d.Statuses},
		CurrentPage: d.CurrentPage, Size: d.PerPage, DefaultSize: d.PerPage,
	}
}

func orderKey(key string) string {
	if key == "" {
		return "created_at"
	}
	return key
}

func orderDir(dir string) string {
	if dir == "asc" {
		return "asc"
	}
	return "desc"
}

// LineRow is one row of the preview or of the refused ones.
type LineRow struct {
	Number int
	Cells  []string
	Reason string
}

// ShowPageData is what the page of a run, and the fragment of its state, are
// drawn from. They share the data: the fragment is the part of the page that
// changes while the run goes, and the page asks for it again until it is over.
type ShowPageData struct {
	Screen
	Run        RunRow
	Definition DefinitionView
	BackURL    string

	// ProgressURL is where the fragment asks again, and Polling says it should.
	ProgressURL string
	Polling     bool

	ConfirmURL, DiscardURL, RefusedURL, FileURL string
	CanConfirm                                  bool
	HasFile, HasReport                          bool

	// Heads are the labels of the definition's columns, in order, and Mapped
	// what the file called each.
	Heads  []string
	Mapped []MappedRow
	// Ignored is the headings of the file no column answers to.
	Ignored string
	// Header says the first row was read as a heading.
	Header bool

	// Valid and Invalid are the counts of the preview.
	Valid, Invalid int
	// Sample is the first rows of the file; Problems the first refused ones, in
	// the preview and in the report.
	Sample      []LineRow
	Problems    []LineRow
	MoreProblem int

	// Failure is why a run failed, in words.
	Failure string
	// Retention is how many days the files are kept.
	Retention int
	// Progress is what the bar needs.
	Indeterminate bool
}

// MappedRow says which heading of the file became which column.
type MappedRow struct {
	Label, Heading string
	Required       bool
	Found          bool
}

func pager(rows, total, perPage, page int, path string, query url.Values) components.Paginator {
	return pagination.NewLengthAwarePage(rows, total, perPage, page, pagination.Options{Path: path, Query: query})
}

// size writes a size in bytes the way a person reads one.
func size(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	}
	return strconv.FormatInt(n, 10) + " B"
}

// StatusVariant is the badge variant of a status.
func StatusVariant(s Status) string {
	switch s {
	case StatusDone:
		return "outline"
	case StatusFailed:
		return "destructive"
	case StatusQueued, StatusRunning:
		return "secondary"
	case StatusDiscarded:
		return "outline"
	}
	return ""
}

// Itoa writes a number for a view, which cannot import strconv under a name of
// its own.
func Itoa(n int) string { return strconv.Itoa(n) }

// Overview says the page is the overview and not the page of one definition.
func (d IndexPageData) Overview() bool { return d.Definition == nil }

// BackURL is where the page of a definition goes back to: the overview.
func (d IndexPageData) BackURL() string { return d.URLs.Index() }

// UploadHint is the line under the file box: what it takes and how much.
func (d IndexPageData) UploadHint() string {
	return d.Labels.F("screen.upload_hint", "mb", d.MaxMB, "rows", d.MaxRows)
}

// ExportLead says what an export does and how long its file is kept.
func (d IndexPageData) ExportLead() string {
	return d.Labels.F("screen.export_lead", "days", d.Retention)
}

// StartedText says when the run was sent, and when it ended.
func (d ShowPageData) StartedText() string {
	text := d.Labels.F("screen.started", "when", d.Run.When)
	if d.Run.Finished != "" {
		text += " · " + d.Labels.F("screen.finished", "when", d.Run.Finished)
	}
	return text
}

// IgnoredText names the headings of the file nothing answered to.
func (d ShowPageData) IgnoredText() string { return d.Labels.F("screen.ignored", "names", d.Ignored) }

// MoreText says how many refused rows the list does not show, and where the
// rest is.
func (d ShowPageData) MoreText(report bool) string {
	if report {
		return d.Labels.F("screen.problems_more_report", "n", d.MoreProblem)
	}
	return d.Labels.F("screen.problems_more", "n", d.MoreProblem)
}

// KeepNote says how long the file is kept.
func (d ShowPageData) KeepNote() string { return d.Labels.F("screen.keep_note", "days", d.Retention) }

// ProgressTitle and ProgressLead are what the card of a run that is going says.
func (d ShowPageData) ProgressTitle() string {
	switch {
	case d.Run.StatusKey == string(StatusQueued):
		return d.Labels.T("screen.queued_title")
	case d.Run.IsExport:
		return d.Labels.T("screen.running_export_title")
	}
	return d.Labels.T("screen.running_title")
}

func (d ShowPageData) ProgressLead() string {
	switch {
	case d.Run.StatusKey == string(StatusQueued):
		return d.Labels.T("screen.queued_lead")
	case d.Run.IsExport:
		return d.Labels.F("screen.running_export_lead", "done", d.Run.Processed)
	}
	return d.Labels.F("screen.running_lead", "done", d.Run.Processed, "total", d.Run.Total)
}

// ExportDoneLead says what a finished export holds and how long it is kept.
func (d ShowPageData) ExportDoneLead() string {
	return d.Labels.F("screen.done_export_lead", "rows", d.Run.Total, "days", d.Retention)
}
