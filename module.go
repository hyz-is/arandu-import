// Package imports is an Arandu module for importing and exporting records from
// files: the person attaches a spreadsheet or a CSV, looks at what the
// application understood, confirms, watches the import run in the background
// and reads a report of what went in and what was refused.
//
// It is dynamic. The application registers a Definition for each kind of
// import -- its columns, how a row is checked and where it is written -- and
// the package draws the screens, reads the file, runs the job and keeps the
// history. Nothing in it knows what a plate, a customer or a product is.
//
// The files are laid out by role rather than by layer, so the whole package
// reads top to bottom:
//
//	module.go      -> registration and routes
//	config.go      -> what the application passes in
//	definition.go  -> what the application states for each kind of import
//	model.go       -> the run, and what it may answer with
//	policy.go      -> who may do what
//	service.go     -> the use cases, and Model access after authorization
//	job.go         -> the work the queue runs
//	handlers.go    -> the routes' handlers
//	views.go       -> the files the application takes ownership of
//
// An application registers it explicitly. There is no service provider, no
// container and no discovery: the wiring is a few lines somebody wrote.
package imports

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/view"
)

// Module is what the application registers.
//
// It implements foundation.Module, which is Name and Routes and nothing else.
// It also implements foundation.Migratable, because it owns a table;
// foundation.Publishable, because it hands view sources to the project;
// foundation.Schedulable, because it declares the clean of the files; and the
// job registration the framework reads from a module that has one.
type Module struct {
	cfg      Config
	service  *Service
	sessions *security.SessionStore
}

// Compile-time proof that the module honors the contracts it claims.
var (
	_ foundation.Module      = (*Module)(nil)
	_ foundation.Migratable  = (*Module)(nil)
	_ foundation.Bootable    = (*Module)(nil)
	_ foundation.Publishable = (*Module)(nil)
	_ foundation.Schedulable = (*Module)(nil)
)

// New returns the module, or the reason it cannot be built.
//
// The collaborators are parameters and not fields somebody fills in afterwards:
// a module that could be registered half-wired is a module whose first request
// is the thing that reports the missing half. It returns an error rather than
// panicking, because everything it refuses is a wiring mistake, and a wiring
// mistake found at boot costs one restart.
func New(cfg Config, db *data.DB, sessions *security.SessionStore) (*Module, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("imports: New needs a database handle: this package owns a table, and there is no in-memory mode that would let it start without one")
	}
	if sessions == nil {
		return nil, errors.New("imports: New needs a session store: every route is mounted behind RequireAuth, which reads it to admit a request and to put who is asking on it")
	}
	cfg = cfg.withDefaults()
	return &Module{cfg: cfg, service: NewService(db, cfg), sessions: sessions}, nil
}

// Name is the module identifier: a lowercase slug, stable, no spaces. It is what
// `aru route:list` groups by and what the route names are prefixed with.
func (m *Module) Name() string { return "imports" }

// Service returns the use cases, for an application that starts an import or an
// export from somewhere other than these routes.
func (m *Module) Service() *Service { return m.service }

// URLs returns the addresses of the module, built from the configuration. An
// application links to the import of a definition with it, and never writes the
// path a second time.
func (m *Module) URLs() URLs { return URLs{cfg: m.cfg} }

// Routes registers the module's routes under the configured prefix. They are
// named, so a URL is built from a name rather than written out a second time.
//
// Every route needs somebody signed in, so the guard is mounted once, on the
// router they are registered on: r is replaced by the guarded group before the
// first registration, and no unguarded router is left in scope for the next
// route to be added to.
func (m *Module) Routes(r *fhttp.Router) {
	guards := []fhttp.Middleware{middleware.RequireAuth(m.sessions)}
	for _, mw := range m.cfg.Middleware {
		guards = append(guards, mw)
	}
	r = r.Group("", guards...)
	handlers := m.handlers()
	for _, rt := range routeTable(m.cfg) {
		r.Action(rt.method, rt.path, handlers[rt.name]).Name(rt.name)
	}
}

// screen builds the page a view is drawn from: the title, the labels, the
// addresses and the frame the application draws around it.
func (m *Module) screen(ctx *fhttp.Context, who security.Subject, title string) (Screen, error) {
	labels := m.Labels()
	page := view.New(ctx, title)
	page.Title = m.documentTitle(title, page.AppName)
	page.Authenticated = who.ID != ""
	sc := Screen{Page: page, Labels: labels, URLs: m.URLs(), Location: m.cfg.Location}
	if m.cfg.Chrome != nil {
		chrome, err := m.cfg.Chrome(ctx, ChromeRequest{Title: title, Subject: who})
		if err != nil {
			return Screen{}, err
		}
		sc.chrome = chrome
	}
	return sc, nil
}

func (m *Module) documentTitle(title, appName string) string {
	if m.cfg.TitleSeparator == "" || strings.TrimSpace(appName) == "" {
		return title
	}
	return title + m.cfg.TitleSeparator + appName
}

// PublishCommand is what an application runs to take ownership of the views
// this package offers. It reads the modules the application registered and
// writes what each one declares.
const PublishCommand = "aru vendor:publish --apply"

// Boot refuses to serve when a view this package renders is not in the binary.
//
// A compiled view registers itself from init(), so by the time anything boots
// the question has one answer already: either the application published the
// files, compiled them and imported the package they became, or it did not.
// Asking here turns "did anybody run the install command" into one refusal at
// start-up that names the views and the command, instead of a 500 on the first
// request that reached one of them.
//
// It also holds the destination. Every file the archive offers has to land
// under the module directory named after this module.
func (m *Module) Boot(context.Context) error {
	prefix := viewRoot + "/" + moduleDir + "/" + m.Name() + "/"
	var stray []string
	for _, path := range PublishedPaths() {
		if !strings.HasPrefix(path, prefix) {
			stray = append(stray, path)
		}
	}
	if len(stray) > 0 {
		return fmt.Errorf("imports: %s would be published outside %s, where it lands on a file the application wrote",
			strings.Join(stray, ", "), prefix)
	}

	registered := make(map[string]bool)
	for _, name := range view.Registered() {
		registered[name] = true
	}
	var missing []string
	for _, name := range ViewNames() {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		imports := make([]string, 0, len(ViewPackages()))
		for _, pkg := range ViewPackages() {
			imports = append(imports, "<module path>/"+pkg)
		}
		return fmt.Errorf("imports: no view is registered as %s. Run `%s`, then `aru view:build`, then import %s in bootstrap/app.go",
			strings.Join(missing, ", "), PublishCommand, strings.Join(imports, ", "))
	}
	return nil
}
