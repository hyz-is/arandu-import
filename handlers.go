package imports

import (
	"errors"
	"io"
	"mime"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/translation"
)

// Handlers are thin on purpose: read the input, ask the service, answer. No
// rule, database handle or Model construction lives here. A handler that
// reached data directly would skip the service's policy boundary, and the
// layout makes that visible rather than relying on review.
//
// Who is asking comes from ctx.User, which reads the subject RequireAuth put on
// the request. An error from the service is returned as it is, and the router
// answers it: validation.Errors goes back where the request came from with the
// messages in the flash, ErrNotFound is 404, and a policy refusal is 403.

func (m *Module) handlers() map[string]func(*fhttp.Context) error {
	return map[string]func(*fhttp.Context) error{
		"imports.index":    m.index,
		"imports.new":      m.definitionPage,
		"imports.upload":   m.upload,
		"imports.template": m.template,
		"imports.export":   m.export,
		"imports.show":     m.show,
		"imports.progress": m.progress,
		"imports.confirm":  m.confirm,
		"imports.discard":  m.discard,
		"imports.refused":  m.refused,
		"imports.file":     m.file,
	}
}

// index is the overview: what the subject may import or export, and the history
// of the space.
func (m *Module) index(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	p := listParams(ctx)
	page, err := m.service.List(ctx.Ctx(), who, p.query("", m.cfg.PageSize))
	if err != nil {
		return err
	}
	return m.listPage(ctx, who, nil, p, page)
}

// definitionPage is the page of one definition.
func (m *Module) definitionPage(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	def, err := m.service.definition(ctx.Param("definition"))
	if err != nil {
		return err
	}
	p := listParams(ctx)
	page, err := m.service.List(ctx.Ctx(), who, p.query(def.Key, m.cfg.PageSize))
	if err != nil {
		return err
	}
	return m.listPage(ctx, who, &def, p, page)
}

// query is the history a page asks for.
func (p params) query(definition string, perPage int) ListQuery {
	return ListQuery{Definition: definition, Search: p.search, Statuses: p.statuses, Sort: p.sort, Dir: p.dir, Page: p.page, PerPage: perPage}
}

func (m *Module) listPage(ctx *fhttp.Context, who security.Subject, def *Definition, p params, page Page) error {
	if ctx.WantsJSON() {
		items := make([]map[string]any, 0, len(page.Rows))
		for _, run := range page.Rows {
			items = append(items, NewResource(*run).ToArray())
		}
		return ctx.JSON(stdhttp.StatusOK, payload{"items": items, "total": page.Total, "page": page.Page, "pages": page.Pages})
	}
	title := m.Labels().T("screen.index_title")
	if def != nil {
		title = def.Label
	}
	sc, err := m.screen(ctx, who, title)
	if err != nil {
		return err
	}
	return ctx.View(ViewIndex, m.indexPage(ctx, sc, who, def, p, page))
}

func (m *Module) upload(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	key := ctx.Param("definition")
	file, name, done, err := m.readFile(ctx)
	if err != nil {
		return err
	}
	defer done()
	run, err := m.service.Upload(ctx.Ctx(), who, key, Upload{Name: name, Body: file})
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, NewResource(*run))
	}
	return ctx.Redirect(m.URLs().Run(run.Definition, run.ID))
}

func (m *Module) template(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	var buf strings.Builder
	name, kind, err := m.service.Template(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("format"), &buf)
	if err != nil {
		return err
	}
	header := ctx.Response.Header()
	header.Set("Content-Type", kind)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	header.Set("Content-Length", strconv.Itoa(buf.Len()))
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.WriteHeader(stdhttp.StatusOK)
	_, err = io.WriteString(ctx.Response, buf.String())
	return err
}

func (m *Module) export(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	if err := ctx.Request.ParseForm(); err != nil {
		return validation.Errors{"format": {m.Labels().message("format")}}
	}
	run, err := m.service.RequestExport(ctx.Ctx(), who, ctx.Param("definition"), strings.TrimSpace(ctx.Request.PostForm.Get("format")))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, NewResource(*run))
	}
	return ctx.Redirect(m.URLs().Run(run.Definition, run.ID))
}

func (m *Module) show(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	run, err := m.service.Find(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewResource(*run))
	}
	sc, err := m.screen(ctx, who, m.Labels().T("screen.show_title"))
	if err != nil {
		return err
	}
	return ctx.View(ViewShow, m.showPage(ctx, sc, who, run))
}

// progress answers the fragment of a run's state, which the page asks for again
// every second while the run is going and stops asking when it is over.
func (m *Module) progress(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	run, err := m.service.Find(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("id"))
	if err != nil {
		return err
	}
	sc, err := m.screen(ctx, who, m.Labels().T("screen.show_title"))
	if err != nil {
		return err
	}
	ctx.Response.Header().Set("Cache-Control", "private, no-store")
	return ctx.Fragment(stdhttp.StatusOK, ViewState, m.showPage(ctx, sc, who, run))
}

func (m *Module) confirm(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	run, err := m.service.Confirm(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusAccepted, NewResource(*run))
	}
	return ctx.Redirect(m.URLs().Run(run.Definition, run.ID))
}

func (m *Module) discard(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	key := ctx.Param("definition")
	if err := m.service.Discard(ctx.Ctx(), who, key, ctx.Param("id")); err != nil {
		return err
	}
	if ctx.WantsJSON() {
		ctx.Response.WriteHeader(stdhttp.StatusNoContent)
		return nil
	}
	return ctx.Redirect(m.URLs().Definition(key))
}

func (m *Module) refused(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	_, body, err := m.service.Open(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("id"), true)
	if err != nil {
		return err
	}
	return serve(ctx, body)
}

func (m *Module) file(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	_, body, err := m.service.Open(ctx.Ctx(), who, ctx.Param("definition"), ctx.Param("id"), false)
	if err != nil {
		return err
	}
	return serve(ctx, body)
}

// serve streams a file as a download. The content type is the one the package
// wrote, never one a client announced, the body is never sniffed and it is
// never shown inline.
func serve(ctx *fhttp.Context, body Download) error {
	defer body.Body.Close()
	header := ctx.Response.Header()
	header.Set("Content-Type", body.ContentType)
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": body.Name}))
	if body.Size > 0 {
		header.Set("Content-Length", strconv.FormatInt(body.Size, 10))
	}
	header.Set("Cache-Control", "private, no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Response.WriteHeader(stdhttp.StatusOK)
	_, err := io.Copy(ctx.Response, body.Body)
	return err
}

// readFile reads the one file of an upload. The body is bounded before it is
// read, so a request cannot make the server hold more than a file may weigh;
// whoever calls it closes what it opened with the function it returns.
func (m *Module) readFile(ctx *fhttp.Context) (io.Reader, string, func(), error) {
	ctx.Request.Body = stdhttp.MaxBytesReader(ctx.Response, ctx.Request.Body, m.cfg.MaxFileBytes+(1<<20))
	if err := ctx.Request.ParseMultipartForm(8 << 20); err != nil {
		var tooLarge *stdhttp.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, "", nil, validation.Errors{"file": {m.Labels().line("validation.file_large", translation.Replace{"max": megabytes(m.cfg.MaxFileBytes)})}}
		}
		return nil, "", nil, validation.Errors{"file": {m.Labels().message("file_missing")}}
	}
	done := func() {
		if ctx.Request.MultipartForm != nil {
			_ = ctx.Request.MultipartForm.RemoveAll()
		}
	}
	headers := ctx.Request.MultipartForm.File["file"]
	if len(headers) == 0 || (headers[0].Filename == "" && headers[0].Size == 0) {
		done()
		return nil, "", nil, validation.Errors{"file": {m.Labels().message("file_missing")}}
	}
	f, err := headers[0].Open()
	if err != nil {
		done()
		return nil, "", nil, err
	}
	closeAll := func() {
		_ = f.Close()
		done()
	}
	return f, headers[0].Filename, closeAll, nil
}

// params are what a history's address says, read and cleaned: a status that is
// not one and a sort the list does not offer are dropped rather than refused,
// because an address somebody edited by hand is not a form to send back.
type params struct {
	search    string
	statuses  []Status
	sort, dir string
	page      int
}

func listParams(ctx *fhttp.Context) params {
	query := ctx.Request.URL.Query()
	p := params{search: strings.TrimSpace(query.Get("q"))}
	for _, raw := range query["status"] {
		if s := Status(strings.TrimSpace(raw)); s.Valid() {
			p.statuses = append(p.statuses, s)
		}
	}
	if sort := query.Get("sort"); sort != "" {
		if _, ok := sortable[sort]; ok {
			p.sort = sort
		}
	}
	if query.Get("dir") == "asc" {
		p.dir = "asc"
	}
	p.page, _ = strconv.Atoi(query.Get("page"))
	return p
}

func (p params) values() url.Values {
	values := url.Values{}
	if p.search != "" {
		values.Set("q", p.search)
	}
	for _, s := range p.statuses {
		values.Add("status", string(s))
	}
	if p.sort != "" {
		values.Set("sort", p.sort)
	}
	if p.dir != "" {
		values.Set("dir", p.dir)
	}
	return values
}

// allows says whether the subject holds a definition's action in its read form.
func (m *Module) allows(ctx *fhttp.Context, who security.Subject, a security.Action) bool {
	return auth.Allows(ctx.Ctx(), m.service.policy, who, viewOf(a), Run{})
}

// definitionView is a definition as the screens show it, with what the subject
// may do with it.
func (m *Module) definitionView(ctx *fhttp.Context, who security.Subject, def Definition) DefinitionView {
	return DefinitionView{
		Key: def.Key, Label: def.Label, Description: def.Description, URL: m.URLs().Definition(def.Key), ListURL: def.ListURL,
		CanImport: def.CanImport() && m.allows(ctx, who, def.ImportAction),
		CanExport: def.CanExport() && m.allows(ctx, who, def.ExportAction),
	}
}

// payload is a JSON answer: the fields a handler chose, under their names.
type payload map[string]any

// ToArray returns the fields.
func (p payload) ToArray() map[string]any { return p }

// With returns nothing beside them.
func (p payload) With() map[string]any { return nil }
