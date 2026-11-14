package imports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/queue/jobs"
	"github.com/arandu-io/hesape/translation"
)

// previewRows and problemRows are how many rows the preview shows and how many
// refused rows the screen lists before it says there are more in the file.
const (
	previewRows = 10
	problemRows = 20
	sampleRows  = 20
)

// core is what the services share: the handle, the configuration, the
// labels, the policy and the clock.
type core struct {
	db     *data.DB
	cfg    Config
	labels Labels
	policy RunPolicy
	now    func() time.Time
}

func newCore(db *data.DB, cfg Config) *core {
	read := cfg.Now
	return &core{
		db: db, cfg: cfg, labels: labelsOf(cfg), policy: NewRunPolicy(cfg.Definitions),
		now: func() time.Time { return read().UTC() },
	}
}

// Service is every use case of the package: it validates the request, asks the
// policy for a Grant, and reaches the database only with it.
type Service struct {
	*core
}

// NewService builds the service over a database handle and a configuration.
func NewService(db *data.DB, cfg Config) *Service {
	return &Service{core: newCore(db, cfg.withDefaults())}
}

// Upload is a file as it arrived.
type Upload struct {
	// Name is what the person's computer called it.
	Name string
	// Body is its content.
	Body io.Reader
}

// definition finds the definition a key names. A key nobody registered is a
// record that does not exist.
func (c *core) definition(key string) (Definition, error) {
	def, ok := c.cfg.definition(key)
	if !ok {
		return Definition{}, ErrNotFound
	}
	return def, nil
}

// Upload reads a file, checks every row of it against the definition and keeps
// it for the person to look at before confirming. Nothing is written to the
// application's tables yet.
//
// What is wrong with the file as a whole -- empty, too large, not a table, a
// required column missing, too many rows -- is returned as validation errors on
// the "file" field. A row that is wrong is not an error: it is in the preview.
func (s *Service) Upload(ctx context.Context, actor security.Subject, key string, in Upload) (*Run, error) {
	def, err := s.definition(key)
	if err != nil {
		return nil, err
	}
	if !def.CanImport() {
		return nil, ErrNotFound
	}
	g, err := security.Authorize(ctx, s.policy, actor, def.ImportAction, Run{})
	if err != nil {
		return nil, err
	}
	if in.Body == nil {
		return nil, s.labels.invalid("file", "file_missing")
	}
	body, err := io.ReadAll(io.LimitReader(in.Body, s.cfg.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > s.cfg.MaxFileBytes {
		return nil, s.labels.invalid("file", "file_large", translation.Replace{"max": megabytes(s.cfg.MaxFileBytes)})
	}

	rows, m, err := s.read(def, in.Name, body)
	if err != nil {
		return nil, err
	}
	preview, err := s.check(ctx, g, def, m, rows)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		return nil, err
	}

	id, err := database.NewOrderedID()
	if err != nil {
		return nil, err
	}
	format := formatOf(in.Name, body)
	fileKey := path.Join("imports", id, "source"+extension(format))
	if err := s.cfg.Files.Put(ctx, g, fileKey, bytes.NewReader(body), contentType(format)); err != nil {
		return nil, err
	}

	run, err := Runs(s.db).New()
	if err != nil {
		s.drop(ctx, g, fileKey)
		return nil, err
	}
	now := s.now()
	run.ID, run.TenantID = id, data.Tenant(g)
	run.Definition, run.Kind, run.Status, run.Format = def.Key, KindImport, string(StatusUploaded), format
	run.FileName, run.FileKey, run.FileSize = fileName(in.Name, extension(format)), fileKey, int64(len(body))
	run.Total, run.Preview, run.ActorID = int64(preview.Total), string(encoded), actor.ID
	run.CreatedAt, run.UpdatedAt, run.ExpiresAt = now, now, now.AddDate(0, 0, s.cfg.RetentionDays)
	if _, err := run.Save(ctx, g); err != nil {
		s.drop(ctx, g, fileKey)
		return nil, err
	}
	s.changed(ctx, run)
	return run, nil
}

// read turns the bytes into the data rows of the definition, or says what is
// wrong with the file as a whole.
func (c *core) read(def Definition, name string, body []byte) ([]gridRow, mapping, error) {
	g, err := readGrid(name, body, def.MaxRows+2)
	if err != nil {
		return nil, mapping{}, c.fileError(err, def)
	}
	m, missing := mapColumns(def.Columns, g.rows[0].cells)
	if len(missing) > 0 {
		return nil, mapping{}, c.labels.invalid("file", "columns_missing", translation.Replace{"columns": strings.Join(missing, ", ")})
	}
	rows := m.data(g.rows)
	switch {
	case len(rows) == 0:
		return nil, mapping{}, c.labels.invalid("file", "file_no_rows")
	case len(rows) > def.MaxRows || g.truncated:
		return nil, mapping{}, c.labels.invalid("file", "too_many_rows", translation.Replace{"max": def.MaxRows})
	}
	return rows, m, nil
}

// fileError words a reading problem.
func (c *core) fileError(err error, def Definition) error {
	if p, ok := err.(problem); ok {
		return c.labels.invalid("file", p.code, translation.Replace{"max": def.MaxRows})
	}
	return err
}

// check runs the definition's Validate over every row, to say before the
// confirmation how many will go in and why the others will not. It writes
// nothing.
func (c *core) check(ctx context.Context, g security.Grant, def Definition, m mapping, rows []gridRow) (Preview, error) {
	p := Preview{Header: m.header, Columns: m.columns, Ignored: m.ignored, Total: len(rows)}
	for _, row := range rows {
		values := m.row(def.Columns, row.cells)
		shown := Line{Number: row.number, Values: values}
		_, err := def.Validate(ctx, g, cloneRow(values))
		if err != nil {
			reason, refused := reasonOf(err)
			if !refused {
				return Preview{}, err
			}
			p.Invalid++
			shown.Reason = reason
			if len(p.Problems) < problemRows {
				p.Problems = append(p.Problems, shown)
			}
		} else {
			p.Valid++
		}
		if len(p.Sample) < previewRows {
			p.Sample = append(p.Sample, shown)
		}
	}
	return p, nil
}

func cloneRow(r Row) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

// formatOf is the format of an upload that readGrid accepted.
func formatOf(name string, body []byte) string {
	if bytes.HasPrefix(body, zipMagic) {
		return FormatXLSX
	}
	return FormatCSV
}

// Confirm queues the import of an upload. The status moves from uploaded to
// queued and the job is pushed in the same transaction, so a job never refers
// to a run that is not there and a run is never queued without its job.
// Confirming twice is the same as confirming once.
func (s *Service) Confirm(ctx context.Context, actor security.Subject, key, id string) (*Run, error) {
	def, err := s.definition(key)
	if err != nil {
		return nil, err
	}
	if !def.CanImport() {
		return nil, ErrNotFound
	}
	g, err := security.Authorize(ctx, s.policy, actor, def.ImportAction, Run{})
	if err != nil {
		return nil, err
	}
	run, err := s.owned(ctx, g, actor, def, def.ImportAction, KindImport, id)
	if err != nil {
		return nil, err
	}
	if run.Current() != StatusUploaded {
		if run.Current() == StatusDiscarded {
			return nil, s.labels.invalid("file", "discarded")
		}
		return run, nil
	}
	if !run.HasFile() {
		return nil, s.labels.invalid("file", "file_gone")
	}
	err = data.Transaction(ctx, s.db, func(ctx context.Context) error {
		moved, err := Runs(s.db).WhereKey(run.ID).Where("status", "=", string(StatusUploaded)).Update(ctx, g, map[string]any{
			"status": string(StatusQueued), "updated_at": s.now(),
		})
		if err != nil {
			return err
		}
		if moved == 0 {
			return nil
		}
		return s.push(ctx, g, ImportJob, run)
	})
	if err != nil {
		return nil, err
	}
	fresh, err := Runs(s.db).WhereKey(run.ID).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if fresh == nil {
		return nil, ErrNotFound
	}
	s.changed(ctx, fresh)
	return fresh, nil
}

// push queues the job of a run under the Grant that authorized it.
func (c *core) push(ctx context.Context, g security.Grant, name string, run *Run) error {
	job, err := jobs.New(g, c.cfg.QueueName, name, message{RunID: run.ID, Definition: run.Definition})
	if err != nil {
		return err
	}
	job.Attributes.Tries = 3
	job.Attributes.Timeout = 30 * time.Minute
	return c.cfg.Queue.Push(ctx, g, job)
}

// Discard throws away an upload nobody confirmed, and its file.
func (s *Service) Discard(ctx context.Context, actor security.Subject, key, id string) error {
	def, err := s.definition(key)
	if err != nil {
		return err
	}
	if !def.CanImport() {
		return ErrNotFound
	}
	g, err := security.Authorize(ctx, s.policy, actor, def.ImportAction, Run{})
	if err != nil {
		return err
	}
	run, err := s.owned(ctx, g, actor, def, def.ImportAction, KindImport, id)
	if err != nil {
		return err
	}
	if !run.CanDiscard() {
		return nil
	}
	moved, err := Runs(s.db).WhereKey(run.ID).Where("status", "=", string(StatusUploaded)).Update(ctx, g, map[string]any{
		"status": string(StatusDiscarded), "file_key": "", "updated_at": s.now(),
	})
	if err != nil {
		return err
	}
	if moved > 0 && run.FileKey != "" {
		s.drop(ctx, g, run.FileKey)
	}
	return nil
}

// RequestExport queues the export of a definition's records in a format. The
// file is written in the background and the run says when it is ready.
func (s *Service) RequestExport(ctx context.Context, actor security.Subject, key, format string) (*Run, error) {
	def, err := s.definition(key)
	if err != nil {
		return nil, err
	}
	if !def.CanExport() {
		return nil, ErrNotFound
	}
	g, err := security.Authorize(ctx, s.policy, actor, def.ExportAction, Run{})
	if err != nil {
		return nil, err
	}
	if format != FormatCSV && format != FormatXLSX {
		return nil, s.labels.invalid("format", "format")
	}
	id, err := database.NewOrderedID()
	if err != nil {
		return nil, err
	}
	run, err := Runs(s.db).New()
	if err != nil {
		return nil, err
	}
	now := s.now()
	run.ID, run.TenantID = id, data.Tenant(g)
	run.Definition, run.Kind, run.Status, run.Format = def.Key, KindExport, string(StatusQueued), format
	run.FileName = s.exportName(def, format, now)
	run.ActorID = actor.ID
	run.CreatedAt, run.UpdatedAt, run.ExpiresAt = now, now, now.AddDate(0, 0, s.cfg.RetentionDays)
	err = data.Transaction(ctx, s.db, func(ctx context.Context) error {
		if _, err := run.Save(ctx, g); err != nil {
			return err
		}
		return s.push(ctx, g, ExportJob, run)
	})
	if err != nil {
		return nil, err
	}
	s.changed(ctx, run)
	return run, nil
}

// exportName is what an export is called when it downloads: the definition and
// the day, in the zone of the screens.
func (c *core) exportName(def Definition, format string, at time.Time) string {
	return c.labels.T("file.export") + "-" + def.Key + "-" + at.In(c.cfg.Location).Format("2006-01-02") + extension(format)
}

// authorizeView asks for the read form of whichever action of the definition
// the subject holds, import first. It is how the screens that show a run, or
// the runs of a definition, find out that the subject may see any.
func (c *core) authorizeView(ctx context.Context, actor security.Subject, def Definition) (security.Grant, error) {
	var first error
	for _, a := range def.actions() {
		g, err := security.Authorize(ctx, c.policy, actor, viewOf(a), Run{})
		if err == nil {
			return g, nil
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		first = security.ErrForbidden
	}
	return security.Grant{}, first
}

// actions are the actions of the definition's two sides that exist.
func (d Definition) actions() []security.Action {
	var out []security.Action
	if d.CanImport() {
		out = append(out, d.ImportAction)
	}
	if d.CanExport() {
		out = append(out, d.ExportAction)
	}
	return out
}

// actionOf is the action of the side a run is on.
func (d Definition) actionOf(kind string) security.Action {
	if kind == KindExport {
		return d.ExportAction
	}
	return d.ImportAction
}

// owned finds one run of the definition, of the kind, in the Grant's tenant,
// and has the policy decide about it. A run that is somebody else's, of
// another kind or that is not there is not found.
func (c *core) owned(ctx context.Context, g security.Grant, actor security.Subject, def Definition, a security.Action, kind, id string) (*Run, error) {
	run, err := Runs(c.db).WhereKey(id).Where("definition", "=", def.Key).Where("kind", "=", kind).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, ErrNotFound
	}
	if _, err := security.Authorize(ctx, c.policy, actor, a, *run); err != nil {
		return nil, ErrNotFound
	}
	return run, nil
}

// Find returns one run for a subject that may see it: the one that holds the
// action of the run's kind.
func (s *Service) Find(ctx context.Context, actor security.Subject, key, id string) (*Run, error) {
	def, err := s.definition(key)
	if err != nil {
		return nil, err
	}
	g, err := s.authorizeView(ctx, actor, def)
	if err != nil {
		return nil, err
	}
	return s.seen(ctx, g, actor, def, id)
}

func (c *core) seen(ctx context.Context, g security.Grant, actor security.Subject, def Definition, id string) (*Run, error) {
	run, err := Runs(c.db).WhereKey(id).Where("definition", "=", def.Key).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, ErrNotFound
	}
	if _, err := security.Authorize(ctx, c.policy, actor, viewOf(def.actionOf(run.Kind)), *run); err != nil {
		return nil, ErrNotFound
	}
	return run, nil
}

// ListQuery is what a page of the history is asked for.
type ListQuery struct {
	// Definition limits the history to one definition; empty lists them all.
	Definition string
	// Search is part of a file's name.
	Search   string
	Statuses []Status
	// Sort is a key of sortable, Dir "asc" or "desc".
	Sort, Dir string
	Page      int
	PerPage   int
}

// Page is a page of the history.
type Page struct {
	Rows []*Run
	// Total is how many runs match, Page the page this is and Pages how many
	// there are.
	Total, Page, Pages int
}

var sortable = map[string]string{
	"": "created_at", "created_at": "created_at", "status": "status", "total": "total",
	"file_name": "file_name", "definition": "definition",
}

// List returns a page of the history of the space: the runs of the definitions
// and the sides the subject holds an action for, newest first.
func (s *Service) List(ctx context.Context, actor security.Subject, q ListQuery) (Page, error) {
	defs := s.cfg.Definitions
	if q.Definition != "" {
		def, err := s.definition(q.Definition)
		if err != nil {
			return Page{}, err
		}
		defs = []Definition{def}
	}
	type pair struct{ definition, kind string }
	var (
		pairs []pair
		g     security.Grant
		first error
		held  bool
	)
	for _, def := range defs {
		for _, side := range []struct {
			kind   string
			action security.Action
			on     bool
		}{{KindImport, def.ImportAction, def.CanImport()}, {KindExport, def.ExportAction, def.CanExport()}} {
			if !side.on {
				continue
			}
			grant, err := security.Authorize(ctx, s.policy, actor, viewOf(side.action), Run{})
			if err != nil {
				if first == nil {
					first = err
				}
				continue
			}
			if !held {
				g, held = grant, true
			}
			pairs = append(pairs, pair{def.Key, side.kind})
		}
	}
	if !held {
		if first == nil {
			first = security.ErrForbidden
		}
		return Page{}, first
	}
	column, ok := sortable[q.Sort]
	if !ok {
		return Page{}, s.labels.invalid("sort", "sort")
	}
	perPage, page := s.paging(q.PerPage, q.Page)
	filtered := func() *RunQuery {
		b := Runs(s.db).Where(func(group *RunQuery) {
			for i, p := range pairs {
				one := func(side *RunQuery) { side.Where("definition", "=", p.definition).Where("kind", "=", p.kind) }
				if i == 0 {
					group.Where(one)
				} else {
					group.OrWhere(one)
				}
			}
		})
		if len(q.Statuses) > 0 {
			values := make([]any, 0, len(q.Statuses))
			for _, st := range q.Statuses {
				if st.Valid() {
					values = append(values, string(st))
				}
			}
			if len(values) > 0 {
				b = b.WhereIn("status", values)
			}
		}
		if term := strings.TrimSpace(q.Search); term != "" {
			b.Base().GetQuery().WhereLike("file_name", "%"+term+"%", false, "and", false)
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page{}, err
	}
	rows := filtered()
	if q.Dir == "asc" {
		rows = rows.OrderBy(column)
	} else {
		rows = rows.OrderByDesc(column)
	}
	found, err := rows.OrderByDesc("id").Offset((page-1)*perPage).Limit(perPage).Get(ctx, g)
	if err != nil {
		return Page{}, err
	}
	return Page{Rows: found, Total: int(total), Page: page, Pages: pagesOf(int(total), perPage)}, nil
}

func (c *core) paging(perPage, page int) (int, int) {
	if perPage <= 0 {
		perPage = c.cfg.PageSize
	}
	if page < 1 {
		page = 1
	}
	return perPage, page
}

func pagesOf(total, perPage int) int {
	if total == 0 || perPage <= 0 {
		return 1
	}
	return (total + perPage - 1) / perPage
}

// Template writes the template of a definition in a format: the headings, and
// the example rows, so the person sees what a row looks like. It returns the
// name the file downloads as and its content type.
func (s *Service) Template(ctx context.Context, actor security.Subject, key, format string, w io.Writer) (name, kind string, err error) {
	def, err := s.definition(key)
	if err != nil {
		return "", "", err
	}
	if !def.CanImport() {
		return "", "", ErrNotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, viewOf(def.ImportAction), Run{}); err != nil {
		return "", "", err
	}
	if format != FormatCSV && format != FormatXLSX {
		return "", "", ErrNotFound
	}
	tw, err := newTableWriter(format, w, def.Label)
	if err != nil {
		return "", "", err
	}
	heads := make([]string, len(def.Columns))
	for i, c := range def.Columns {
		heads[i] = c.Label
	}
	if err := tw.Header(heads); err != nil {
		return "", "", err
	}
	for _, example := range templateRows(def) {
		cells := make([]string, len(def.Columns))
		for i, c := range def.Columns {
			cells[i] = example[c.Key]
		}
		if err := tw.Row(cells); err != nil {
			return "", "", err
		}
	}
	if err := tw.Close(); err != nil {
		return "", "", err
	}
	return s.labels.T("file.template") + "-" + def.Key + extension(format), contentType(format), nil
}

// templateRows are the examples the template shows.
func templateRows(def Definition) []Row {
	if len(def.Examples) > 0 {
		return def.Examples
	}
	one := Row{}
	has := false
	for _, c := range def.Columns {
		one[c.Key] = c.Example
		has = has || c.Example != ""
	}
	if !has {
		return nil
	}
	return []Row{one}
}

// Open returns a file of a run: the report of the refused rows, or the file the
// run read or wrote. The read is authorized, and written to the activity log.
func (s *Service) Open(ctx context.Context, actor security.Subject, key, id string, report bool) (*Run, Download, error) {
	def, err := s.definition(key)
	if err != nil {
		return nil, Download{}, err
	}
	g, err := s.authorizeView(ctx, actor, def)
	if err != nil {
		return nil, Download{}, err
	}
	run, err := s.seen(ctx, g, actor, def, id)
	if err != nil {
		return nil, Download{}, err
	}
	fileKey, name := run.FileKey, run.FileName
	if report {
		fileKey, name = run.ReportKey, s.labels.T("file.refused")+"-"+strings.TrimSuffix(run.FileName, path.Ext(run.FileName))+".csv"
	}
	if fileKey == "" {
		return nil, Download{}, ErrNotFound
	}
	body, err := s.cfg.Files.Get(ctx, g, fileKey)
	if err != nil {
		return nil, Download{}, err
	}
	kind := contentType(run.Format)
	if report {
		kind = contentType(FormatCSV)
	}
	if err := s.record(ctx, actor, ActivityFileRead, run, map[string]any{"report": report}); err != nil {
		_ = body.Body.Close()
		return nil, Download{}, err
	}
	return run, Download{Name: name, ContentType: kind, Size: body.Size, Body: body.Body}, nil
}

// Download is a file of a run, ready to be served: the name it downloads as,
// the content type the package wrote for it and its bytes. The caller closes
// Body.
type Download struct {
	Name, ContentType string
	Size              int64
	Body              io.ReadCloser
}

// drop deletes a file nothing refers to, and says so in the log when it cannot.
func (c *core) drop(ctx context.Context, g security.Grant, key string) {
	if key == "" {
		return
	}
	if err := c.cfg.Files.Delete(ctx, g, key); err != nil {
		c.cfg.Logger.Warn("imports: removing a file nothing refers to", "key", key, "error", err)
	}
}

// changed tells the application that a run moved.
func (c *core) changed(ctx context.Context, run *Run) {
	if c.cfg.OnChange == nil || run == nil {
		return
	}
	c.cfg.OnChange(ctx, Change{
		Tenant: run.TenantID, Run: run.ID, Definition: run.Definition, Kind: run.Kind, Status: run.Status,
		Processed: run.Processed, Total: run.Total,
	})
}

// message is the payload of a job: which run, and of which definition.
type message struct {
	RunID      string `json:"run_id"`
	Definition string `json:"definition"`
}

// fileName is a name safe to show and to put in a header: the last segment of
// what the person's computer sent, without control characters, and short.
func fileName(given, extension string) string {
	given = strings.ReplaceAll(given, `\`, "/")
	given = path.Base(given)
	if given == "." || given == "/" {
		given = ""
	}
	given = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, given)
	given = strings.TrimSpace(given)
	if given == "" {
		return "arquivo" + extension
	}
	if utf8.RuneCountInString(given) > maxNameLen {
		given = string([]rune(given)[:maxNameLen])
	}
	return given
}

func megabytes(n int64) string {
	mb := float64(n) / float64(1<<20)
	if mb == float64(int64(mb)) {
		return fmt.Sprintf("%d", int64(mb))
	}
	return fmt.Sprintf("%.1f", mb)
}
