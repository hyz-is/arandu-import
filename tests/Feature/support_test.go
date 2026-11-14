package feature_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	hedatabase "github.com/arandu-io/hesape/database"
	sqliteconnector "github.com/arandu-io/hesape/database/connectors/sqlite"
	"github.com/arandu-io/hesape/events"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/queue"
	activitylog "github.com/hyz-is/arandu-activitylog"

	imports "github.com/hyz-is/arandu-import"
)

// dsn opens a database file the way the tests need it: the write lock taken
// when a transaction begins, readers beside a writer, and a wait instead of an
// error when the lock is held.
const dsn = "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(20000)"

// appKey is the key a session store is built over.
const appKey = "0123456789abcdef0123456789abcdef"

const (
	importAction security.Action = "plates.import"
	exportAction security.Action = "plates.export"
)

// store is what the definition of these tests writes to: the application's own
// table, in memory, by tenant.
type store struct {
	mu     sync.Mutex
	rows   map[string]map[string]string // tenant -> plate -> note
	writes int
	// failOn makes Write return an error that is not a refusal when it sees the
	// plate.
	failOn string
}

func (s *store) get(tenant string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.rows[tenant] {
		out[k] = v
	}
	return out
}

// definition is a list of plates: the one the application of these tests offers.
func (s *store) definition() imports.Definition {
	return imports.Definition{
		Key: "plates", Label: "Plates", Description: "The plates you allow.",
		Columns: []imports.Column{
			{Key: "plate", Label: "Placa", Required: true, Headers: []string{"Placas"}, Example: "ABC1D23"},
			{Key: "note", Label: "Nota", Headers: []string{"Observação"}},
		},
		MaxRows:      200,
		ImportAction: importAction, ExportAction: exportAction,
		Validate: func(_ context.Context, _ security.Grant, row imports.Row) (imports.Row, error) {
			plate := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(row["plate"], "-", ""), " ", ""))
			if len(plate) != 7 {
				return nil, imports.Refuse("Placa inválida")
			}
			row["plate"] = plate
			return row, nil
		},
		Write: func(_ context.Context, g security.Grant, row imports.Row) (imports.Outcome, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if row["plate"] == s.failOn {
				return 0, io.ErrUnexpectedEOF
			}
			tenant := security.Tenant(g)
			if s.rows[tenant] == nil {
				s.rows[tenant] = map[string]string{}
			}
			s.writes++
			old, had := s.rows[tenant][row["plate"]]
			note := row["note"]
			if note == "" {
				note = old
			}
			s.rows[tenant][row["plate"]] = note
			switch {
			case !had:
				return imports.Created, nil
			case old == note:
				return imports.Unchanged, nil
			}
			return imports.Updated, nil
		},
		Export: &imports.Export{
			Each: func(_ context.Context, g security.Grant, emit func(imports.Row) error) error {
				for plate, note := range s.get(security.Tenant(g)) {
					if err := emit(imports.Row{"plate": plate, "note": note}); err != nil {
						return err
					}
				}
				return nil
			},
		},
	}
}

// changes records what OnChange was told, by run.
type changes struct {
	mu  sync.Mutex
	all []imports.Change
}

func (c *changes) hear(_ context.Context, change imports.Change) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.all = append(c.all, change)
}

func (c *changes) of(run string) []imports.Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []imports.Change
	for _, change := range c.all {
		if change.Run == run {
			out = append(out, change)
		}
	}
	return out
}

// clock is a time the tests move by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// world is one application with this package installed over a real database,
// a real queue and a real Disk.
type world struct {
	db       *data.DB
	sessions *security.SessionStore
	module   *imports.Module
	service  *imports.Service
	store    *store
	disk     *filesystem.Disk
	worker   *queue.Worker
	jobs     *queue.DatabaseQueue
	changes  *changes
	clock    *clock
	activity *activitylog.Module
}

func install(t *testing.T) *world {
	t.Helper()
	return installWith(t, nil)
}

func installWith(t *testing.T, change func(*imports.Config)) *world {
	t.Helper()
	path := filepath.Join(t.TempDir(), "imports.sqlite")
	handle, err := sql.Open(sqliteconnector.SQLiteConnector{}.DriverName(), "file:"+path+dsn)
	if err != nil {
		t.Fatalf("opening SQLite: %v", err)
	}
	handle.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = handle.Close() })
	connection := hedatabase.NewConnection(handle, path, "", map[string]any{"driver": string(hedatabase.DialectSQLite), "name": "imports-test"})
	db := data.Wrap(handle, data.DialectSQLite)
	sessions := security.NewSessionStore([]byte(appKey), time.Hour, false, security.NewMemoryBackend())

	activity, err := activitylog.New(activitylog.Config{Tenant: "platform"}, db, sessions)
	if err != nil {
		t.Fatalf("building arandu-activitylog: %v", err)
	}
	adapter, err := filesystem.NewLocalFilesystemAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("building a local Disk: %v", err)
	}
	disk := filesystem.NewDisk("imports", adapter)
	jobs := queue.NewDatabaseQueue(db)
	st := &store{rows: map[string]map[string]string{}}
	heard := &changes{}
	held := &clock{now: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}

	cfg := imports.Config{
		Definitions: []imports.Definition{st.definition()},
		Queue:       jobs, Files: disk, Activity: activity.Logger(),
		OnChange: heard.hear, ProgressEvery: 1, RetentionDays: 7,
		Now: held.Now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if change != nil {
		change(&cfg)
	}
	module, err := imports.New(cfg, db, sessions)
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	schema := hedatabase.ForMigrations(connection)
	all := append(append(jobs.Migrations(), events.NewModule().Migrations()...), activity.Migrations()...)
	for _, m := range append(all, module.Migrations()...) {
		if err := m.Up(context.Background(), schema); err != nil {
			t.Fatalf("applying %s: %v", m.GetName(), err)
		}
	}
	worker := queue.NewWorker(jobs, queue.WorkerOptions{Queue: imports.DefaultQueue, Sleep: time.Millisecond})
	if err := module.RegisterJobHandlers(worker); err != nil {
		t.Fatal(err)
	}
	return &world{db: db, sessions: sessions, module: module, service: module.Service(), store: st, disk: disk,
		worker: worker, jobs: jobs, changes: heard, clock: held, activity: activity}
}

// work runs the next job on the queue.
func (w *world) work(t *testing.T) {
	t.Helper()
	if err := w.worker.RunNextJob(context.Background()); err != nil {
		t.Fatalf("running a job: %v", err)
	}
}

// pending is how many jobs wait.
func (w *world) pending(t *testing.T) int {
	t.Helper()
	n, err := w.jobs.Size(context.Background(), imports.DefaultQueue)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// who is a subject of a tenant holding the actions.
func who(tenant, id string, actions ...security.Action) security.Subject {
	return security.Subject{ID: id, Tenant: tenant, Roles: []string{"member"}, Actions: actions, Verified: true}
}

var (
	ana   = who("acme", "ana", importAction, exportAction)
	bruno = who("globex", "bruno", importAction, exportAction)
)

// upload sends a file through the service as a subject.
func (w *world) upload(t *testing.T, actor security.Subject, name string, body []byte) *imports.Run {
	t.Helper()
	run, err := w.service.Upload(context.Background(), actor, "plates", imports.Upload{Name: name, Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatalf("uploading %s: %v", name, err)
	}
	return run
}

// confirmAndRun confirms an upload and lets the worker do it.
func (w *world) confirmAndRun(t *testing.T, actor security.Subject, id string) *imports.Run {
	t.Helper()
	if _, err := w.service.Confirm(context.Background(), actor, "plates", id); err != nil {
		t.Fatalf("confirming: %v", err)
	}
	w.work(t)
	run, err := w.service.Find(context.Background(), actor, "plates", id)
	if err != nil {
		t.Fatalf("finding the run: %v", err)
	}
	return run
}
