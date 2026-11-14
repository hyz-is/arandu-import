package imports

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/queue"
	"github.com/arandu-io/hesape/translation"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// The defaults and the ceilings of the optional settings. They are constants
// rather than literals inside Config.withDefaults, so the value a reader finds
// here is the value the package uses.
const (
	// DefaultPrefix is where the routes are mounted when Config leaves Prefix
	// empty.
	DefaultPrefix = "/imports"
	// DefaultQueue is the queue the jobs travel on when Config leaves QueueName
	// empty.
	DefaultQueue = "default"
	// DefaultPageSize is how many runs one page of the history answers with.
	DefaultPageSize = 25
	// MaxPageSize is the ceiling PageSize is refused above.
	MaxPageSize = 100
	// DefaultMaxFileBytes is the largest file an import takes when Config
	// leaves MaxFileBytes at zero.
	DefaultMaxFileBytes = 10 << 20
	// MaxFileBytesLimit is the ceiling MaxFileBytes is refused above: the whole
	// file is read into memory to be checked, so it has to stay small.
	MaxFileBytesLimit = 50 << 20
	// DefaultMaxRows is how many rows one import takes when its Definition
	// leaves MaxRows at zero.
	DefaultMaxRows = 5000
	// MaxRowsLimit is the ceiling a Definition's MaxRows is refused above.
	MaxRowsLimit = 100000
	// DefaultExportRows is how many rows one export holds when its Export
	// leaves MaxRows at zero.
	DefaultExportRows = 50000
	// DefaultRetentionDays is how long the files of a run are kept when Config
	// leaves RetentionDays at zero.
	DefaultRetentionDays = 7
	// MaxRetentionDays is the ceiling RetentionDays is refused above.
	MaxRetentionDays = 90
	// DefaultCleanSpec is when the scheduled clean runs: every day at 03:20.
	DefaultCleanSpec = "20 3 * * *"
	// DefaultProgressEvery is how many rows a job reads between two writes of
	// its progress.
	DefaultProgressEvery = 50
	// DefaultLocale is the language of the screens when Config names none, and
	// FallbackLocale the one every line exists in.
	DefaultLocale  = "pt-BR"
	FallbackLocale = "en"
	// DefaultLocation is the time zone dates are drawn in when Config names
	// none.
	DefaultLocation = "America/Sao_Paulo"
)

// reservedPrefix is the part of the address space the framework keeps for
// itself.
const reservedPrefix = "/_arandu"

// Config is what the application passes when it wires this package.
//
// A typed struct rather than a map: a misspelled key in a map is a setting that
// silently keeps its default. Here a field that does not exist does not
// compile.
//
// It names no tenant. Every route sits behind RequireAuth, so the tenant of
// every statement is the one the Grant carries, and the job that runs later
// takes it from the row the Grant wrote.
type Config struct {
	// Definitions are the kinds of import and export the application offers,
	// each with its columns, how a row is checked and where it is written.
	// At least one is required.
	Definitions []Definition

	// Queue is where the jobs go. Required: an import runs in the background.
	Queue queue.Queue

	// QueueName is the queue the jobs are pushed on. Empty means DefaultQueue.
	QueueName string

	// Files is the Disk the uploaded files, the reports and the exports are
	// kept on, in production a private bucket. Required. The keys are generated
	// by the server and a file is read back only through an authorized route.
	Files *filesystem.Disk

	// Prefix is the path the routes are mounted under. Empty means
	// DefaultPrefix.
	Prefix string

	// Paths are the segments of the routes under the prefix. A zero field keeps
	// its default.
	Paths Paths

	// Middleware wraps every route, after RequireAuth.
	Middleware []func(http.Handler) http.Handler

	// Activity is where the start and the end of every import and export are
	// written, with the counts and never the content of a row. Nil writes
	// nothing; an application with several tenants passes it.
	Activity *activitylog.Logger

	// OnChange is told after every write of a run's progress and every change
	// of its status, so the application can tell the sockets that watch a table.
	// It is called from the web process and from the worker, and what it does
	// fails silently: a run does not depend on it.
	OnChange func(ctx context.Context, change Change)

	// MaxFileBytes is the largest upload, in bytes. Zero means
	// DefaultMaxFileBytes; anything above MaxFileBytesLimit is refused.
	MaxFileBytes int64

	// RetentionDays is how long the files of a run are kept before the clean
	// deletes them. Zero means DefaultRetentionDays.
	RetentionDays int

	// CleanSpec is the five-field cron expression the clean runs on. Empty
	// means DefaultCleanSpec; "-" schedules none.
	CleanSpec string

	// PageSize is how many runs one page of the history holds. Zero means
	// DefaultPageSize.
	PageSize int

	// ProgressEvery is how many rows a job reads between two writes of its
	// progress. Zero means DefaultProgressEvery.
	ProgressEvery int

	// Translator holds the application's own lines, which win over the shipped
	// ones; nil uses the shipped catalogue alone.
	Translator *translation.Translator

	// Locale is the language of the screens. Empty means DefaultLocale.
	Locale string

	// TitleSeparator ends a page title with the application's name, as in
	// "Importar placas — Alpr.sh". Empty writes the title alone.
	TitleSeparator string

	// Chrome draws the application's frame around a screen: it returns what the
	// application's layout reads from the screen. Nil leaves the layout to the
	// page alone.
	Chrome func(ctx *fhttp.Context, req ChromeRequest) (any, error)

	// Location is the time zone dates are drawn in. Nil means DefaultLocation.
	Location *time.Location

	// Now is the clock; nil means time.Now.
	Now func() time.Time

	// Logger receives what the package reports. Nil means slog.Default.
	Logger *slog.Logger
}

// ChromeRequest is what the application is asked to draw a frame for.
type ChromeRequest struct {
	// Title is the screen's title.
	Title string
	// Subject is who is looking at it.
	Subject security.Subject
}

// Change is what a run just did, as OnChange is told.
type Change struct {
	// Tenant is the space the run belongs to.
	Tenant string
	// Run is the run's identifier and Definition the key of its definition.
	Run, Definition string
	// Kind is KindImport or KindExport, Status the status it is in now.
	Kind, Status string
	// Processed and Total say how far along it is.
	Processed, Total int64
}

// Paths are the segments of the routes under Prefix, so an application that
// speaks another language renames them without touching the package.
type Paths struct {
	Runs, Template, Export, Progress, Confirm, Discard, Refused, File string
}

// DefaultPaths are the segments in English.
var DefaultPaths = Paths{
	Runs: "/runs", Template: "/template", Export: "/export", Progress: "/progress", Confirm: "/confirm",
	Discard: "/discard", Refused: "/refused", File: "/file",
}

// PortuguesePaths are the segments in Brazilian Portuguese.
var PortuguesePaths = Paths{
	Runs: "/execucoes", Template: "/modelo", Export: "/exportar", Progress: "/progresso", Confirm: "/confirmar",
	Discard: "/descartar", Refused: "/recusadas", File: "/arquivo",
}

func (p *Paths) fields() []struct {
	name  string
	value *string
} {
	return []struct {
		name  string
		value *string
	}{
		{"Runs", &p.Runs}, {"Template", &p.Template}, {"Export", &p.Export}, {"Progress", &p.Progress}, {"Confirm", &p.Confirm},
		{"Discard", &p.Discard}, {"Refused", &p.Refused}, {"File", &p.File},
	}
}

func (p Paths) validate() error {
	seen := map[string]string{}
	for _, f := range p.fields() {
		v := *f.value
		if v == "" {
			continue
		}
		if v[0] != '/' || strings.ContainsAny(v, "{} \t?#") || v == "/" || strings.HasSuffix(v, "/") || strings.Count(v, "/") != 1 {
			return fmt.Errorf("imports: Config.Paths.%s is %q: a segment starts with /, ends without one and holds no wildcard", f.name, v)
		}
		if other, dup := seen[v]; dup {
			return fmt.Errorf("imports: Config.Paths.%s and Config.Paths.%s are both %q", other, f.name, v)
		}
		seen[v] = f.name
	}
	return nil
}

func (p Paths) withDefaults() Paths {
	d := DefaultPaths
	defaults := d.fields()
	for i, f := range p.fields() {
		if *f.value == "" {
			*f.value = *defaults[i].value
		}
	}
	return p
}

// Validate reports what the configuration cannot be used with. New calls it, so
// an application with a setting that cannot work fails where it is wired.
func (c Config) Validate() error {
	if len(c.Definitions) == 0 {
		return fmt.Errorf("imports: Config.Definitions is empty: the package offers nothing to import or export until the application registers a Definition")
	}
	if err := validateDefinitions(c.Definitions); err != nil {
		return err
	}
	if c.Queue == nil {
		return fmt.Errorf("imports: Config.Queue is nil: an import runs in the background, and there is nowhere to send it")
	}
	if c.Files == nil {
		return fmt.Errorf("imports: Config.Files is nil: the uploaded file, the report and the export have to be kept somewhere, and the package never writes to the server's own disk")
	}
	if c.Prefix != "" {
		if c.Prefix[0] != '/' || (len(c.Prefix) > 1 && strings.HasSuffix(c.Prefix, "/")) {
			return fmt.Errorf("imports: Config.Prefix is %q and has to start with / and end without one", c.Prefix)
		}
		if c.Prefix == reservedPrefix || strings.HasPrefix(c.Prefix, reservedPrefix+"/") {
			return fmt.Errorf("imports: Config.Prefix is %q, under %s/, which the framework keeps for itself", c.Prefix, reservedPrefix)
		}
	}
	if err := c.Paths.validate(); err != nil {
		return err
	}
	for i, mw := range c.Middleware {
		if mw == nil {
			return fmt.Errorf("imports: Config.Middleware[%d] is nil", i)
		}
	}
	switch {
	case c.PageSize < 0 || c.PageSize > MaxPageSize:
		return fmt.Errorf("imports: Config.PageSize is %d, and has to be between 0 and %d, where 0 means %d", c.PageSize, MaxPageSize, DefaultPageSize)
	case c.MaxFileBytes < 0 || c.MaxFileBytes > MaxFileBytesLimit:
		return fmt.Errorf("imports: Config.MaxFileBytes is %d, and has to be between 0 and %d, where 0 means %d", c.MaxFileBytes, MaxFileBytesLimit, DefaultMaxFileBytes)
	case c.RetentionDays < 0 || c.RetentionDays > MaxRetentionDays:
		return fmt.Errorf("imports: Config.RetentionDays is %d, and has to be between 0 and %d, where 0 means %d", c.RetentionDays, MaxRetentionDays, DefaultRetentionDays)
	case c.ProgressEvery < 0:
		return fmt.Errorf("imports: Config.ProgressEvery is %d, and cannot be negative", c.ProgressEvery)
	}
	return validateRoutes(c.withDefaults())
}

// validateRoutes asks the standard library to parse the exact patterns the
// module registers. Its parser reports an invalid or conflicting pattern by
// panic, and the throwaway mux turns that boot-time panic into the
// configuration error New promises.
func validateRoutes(c Config) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("imports: Config.Prefix %q and Config.Paths cannot be registered as routes: %v", c.Prefix, recovered)
		}
	}()
	handler := http.NotFoundHandler()
	mux := http.NewServeMux()
	for _, route := range routeTable(c) {
		mux.Handle(route.method+" "+route.path, handler)
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	if c.QueueName == "" {
		c.QueueName = DefaultQueue
	}
	c.Paths = c.Paths.withDefaults()
	if c.PageSize == 0 {
		c.PageSize = DefaultPageSize
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = DefaultMaxFileBytes
	}
	if c.RetentionDays == 0 {
		c.RetentionDays = DefaultRetentionDays
	}
	if c.CleanSpec == "" {
		c.CleanSpec = DefaultCleanSpec
	}
	if c.ProgressEvery == 0 {
		c.ProgressEvery = DefaultProgressEvery
	}
	if c.Locale == "" {
		c.Locale = DefaultLocale
	}
	if c.Location == nil {
		if loc, err := time.LoadLocation(DefaultLocation); err == nil {
			c.Location = loc
		} else {
			c.Location = time.FixedZone("BRT", -3*60*60)
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	defs := make([]Definition, len(c.Definitions))
	for i, d := range c.Definitions {
		defs[i] = d.withDefaults()
	}
	c.Definitions = defs
	return c
}

// route is one entry of the table the module registers.
type route struct {
	name   string
	method string
	path   string
}

// routeTable is every route, written once: the registration, the validation of
// the prefix and the URL builders all read it.
func routeTable(c Config) []route {
	p, s := c.Prefix, c.Paths
	run := p + "/{definition}" + s.Runs + "/{id}"
	return []route{
		{"imports.index", http.MethodGet, p},
		{"imports.new", http.MethodGet, p + "/{definition}"},
		{"imports.upload", http.MethodPost, p + "/{definition}"},
		{"imports.template", http.MethodGet, p + "/{definition}" + s.Template + "/{format}"},
		{"imports.export", http.MethodPost, p + "/{definition}" + s.Export},
		{"imports.show", http.MethodGet, run},
		{"imports.progress", http.MethodGet, run + s.Progress},
		{"imports.confirm", http.MethodPost, run + s.Confirm},
		{"imports.discard", http.MethodPost, run + s.Discard},
		{"imports.refused", http.MethodGet, run + s.Refused},
		{"imports.file", http.MethodGet, run + s.File},
	}
}
