package unit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/filesystem"
	"github.com/arandu-io/hesape/queue"

	imports "github.com/hyz-is/arandu-import"
)

// The properties this package exists to keep are checked here against the code:
//
//  1. the policy denies every action it was not told about, and an action it
//     was told about needs the subject to hold it;
//  2. the service authorizes before constructing or running the generated query;
//  3. the tenant comes from the Grant;
//  4. nothing reaches the database without passing through the first two.
//
// The fourth is checked by the handle these tests pass in. It wraps a nil
// *sql.DB, so any statement that were issued would panic and fail the test
// loudly.

const (
	importAction security.Action = "plates.import"
	exportAction security.Action = "plates.export"
)

// definitions is the smallest registration that is a valid one: it imports and
// exports.
func definitions() []imports.Definition {
	return []imports.Definition{{
		Key: "plates", Label: "Plates",
		Columns:      []imports.Column{{Key: "plate", Label: "Plate", Required: true}, {Key: "note", Label: "Note"}},
		ImportAction: importAction, ExportAction: exportAction,
		Write: func(context.Context, security.Grant, imports.Row) (imports.Outcome, error) {
			return imports.Created, nil
		},
		Export: &imports.Export{
			Each: func(context.Context, security.Grant, func(imports.Row) error) error { return nil },
		},
	}}
}

func nilHandle() *data.DB { return data.Wrap(nil, data.DialectSQLite) }

func disk(t *testing.T) *filesystem.Disk {
	t.Helper()
	adapter, err := filesystem.NewLocalFilesystemAdapter(t.TempDir())
	if err != nil {
		t.Fatalf("building a local Disk: %v", err)
	}
	return filesystem.NewDisk("imports", adapter)
}

func serviceOver(t *testing.T, db *data.DB) *imports.Service {
	t.Helper()
	return imports.NewService(db, imports.Config{Definitions: definitions(), Files: disk(t)})
}

func holder(actions ...security.Action) security.Subject {
	return security.Subject{ID: "user-1", Tenant: "acme", Roles: []string{"admin"}, Actions: actions, Verified: true}
}

func TestThePolicyDeniesWhatNoDefinitionDeclares(t *testing.T) {
	t.Parallel()
	policy := imports.NewRunPolicy(definitions())
	ctx := context.Background()

	// An administrator by role holds no action it was not given.
	for _, a := range []security.Action{"plates.delete", "imports.clean", "anything"} {
		if _, err := security.Authorize(ctx, policy, holder(a), a, imports.Run{}); !errors.Is(err, security.ErrForbidden) {
			t.Errorf("an action no definition declares (%s) was not refused: %v", a, err)
		}
	}
	if _, err := security.Authorize(ctx, policy, holder(), importAction, imports.Run{}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a declared action the subject does not hold was not refused: %v", err)
	}
	if _, err := security.Authorize(ctx, policy, holder(importAction), importAction, imports.Run{}); err != nil {
		t.Errorf("the subject holds the action and was refused: %v", err)
	}
	if _, err := security.Authorize(ctx, policy, holder(importAction), exportAction, imports.Run{}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("holding the import action opened the export one: %v", err)
	}
}

func TestThePolicyDeniesARunOfAnotherTenantAndAGuest(t *testing.T) {
	t.Parallel()
	policy := imports.NewRunPolicy(definitions())
	ctx := context.Background()

	other := imports.Run{ID: "r1", TenantID: "globex"}
	if _, err := security.Authorize(ctx, policy, holder(importAction), importAction, other); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a run of another tenant was not refused: %v", err)
	}
	if _, err := security.Authorize(ctx, policy, security.Guest("acme"), importAction, imports.Run{}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a guest was not refused: %v", err)
	}
}

func TestOnlyTheReadFormsOfAnActionAreReadOnly(t *testing.T) {
	t.Parallel()
	policy := imports.NewRunPolicy(definitions())
	if !policy.Writes(importAction) || !policy.Writes(exportAction) {
		t.Error("starting an import or an export was said not to write")
	}
	if policy.Writes(imports.ViewOf(importAction)) || policy.Writes(imports.ViewOf(exportAction)) {
		t.Error("looking at a run was said to write")
	}
}

func TestTheServiceRefusesBeforeReachingTheModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := serviceOver(t, nilHandle())
	nobody := holder()

	expectForbidden := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, security.ErrForbidden) {
			t.Errorf("%s: want ErrForbidden before any statement, got %v", name, err)
		}
	}
	_, err := s.Upload(ctx, nobody, "plates", imports.Upload{Name: "a.csv", Body: strings.NewReader("plate\nABC1D23\n")})
	expectForbidden("Upload", err)
	_, err = s.Confirm(ctx, nobody, "plates", "r1")
	expectForbidden("Confirm", err)
	expectForbidden("Discard", s.Discard(ctx, nobody, "plates", "r1"))
	_, err = s.RequestExport(ctx, nobody, "plates", "csv")
	expectForbidden("RequestExport", err)
	_, err = s.Find(ctx, nobody, "plates", "r1")
	expectForbidden("Find", err)
	_, err = s.List(ctx, nobody, imports.ListQuery{})
	expectForbidden("List", err)
	_, _, err = s.Open(ctx, nobody, "plates", "r1", true)
	expectForbidden("Open", err)
	_, _, err = s.Template(ctx, nobody, "plates", "csv", &strings.Builder{})
	expectForbidden("Template", err)

	// A definition nobody registered is not found, and says nothing else.
	_, err = s.Find(ctx, holder(importAction), "nope", "r1")
	if !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("an unknown definition: want ErrNotFound, got %v", err)
	}
}

func TestTheConfigurationRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()
	good := func() imports.Config {
		return imports.Config{Definitions: definitions(), Files: disk(t), Queue: queue.NewDatabaseQueue(nilHandle())}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("the base configuration was refused: %v", err)
	}

	cases := map[string]func(*imports.Config){
		"no definitions": func(c *imports.Config) { c.Definitions = nil },
		"no files":       func(c *imports.Config) { c.Files = nil },
		"no queue":       func(c *imports.Config) { c.Queue = nil },
		"bad prefix":     func(c *imports.Config) { c.Prefix = "no-slash" },
		"reserved":       func(c *imports.Config) { c.Prefix = "/_arandu/imports" },
		"page size":      func(c *imports.Config) { c.PageSize = 1000 },
		"file size":      func(c *imports.Config) { c.MaxFileBytes = 1 << 40 },
		"retention":      func(c *imports.Config) { c.RetentionDays = 1000 },
		"duplicate key":  func(c *imports.Config) { c.Definitions = append(c.Definitions, c.Definitions[0]) },
		"no import action": func(c *imports.Config) {
			c.Definitions[0].ImportAction = ""
		},
		"no required column": func(c *imports.Config) {
			c.Definitions[0].Columns = []imports.Column{{Key: "plate", Label: "Plate"}}
		},
		"bad key": func(c *imports.Config) { c.Definitions[0].Key = "Has Space" },
		"clashing paths": func(c *imports.Config) {
			c.Paths = imports.Paths{Template: "/x", Export: "/x"}
		},
	}
	for name, change := range cases {
		cfg := good()
		change(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: the configuration was accepted", name)
		}
	}
}
