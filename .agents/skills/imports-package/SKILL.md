---
name: imports-package
description: Install, wire and use the Arandu Import package (Go, Arandu) in an application. Use when the request is to "install Arandu Import", "import a spreadsheet or CSV", "bulk import", "export to CSV or XLSX", "add an import to the app", "go get github.com/hyz-is/arandu-import", "wire it into bootstrap/app.go", "register a Definition", "register the job handlers", "the import never leaves the queue", "403 forbidden from imports", "it redirects to /auth/login", "the table does not exist", "no such table", or when a project's go.mod already requires github.com/hyz-is/arandu-import. Covers the wiring, the Definition an application writes for each kind of import, the Config fields, the routes, the worker, the files and their clean, and the migration step that is not optional.
license: MIT
metadata:
  audience: app
---

# Using Arandu Import

An Arandu package that imports and exports records from files. The person
attaches a CSV or an XLSX, sees what the application understood, confirms, and
watches the import run in the background; a report says what went in and what
was refused. It is dynamic: the application writes a `Definition` for each kind
of import, and the package draws the screens, reads the file, runs the job and
keeps the history. It is registered by hand — there is no service provider, no
container and no discovery.

## Install

```bash
go get github.com/hyz-is/arandu-import
```

## The wiring, and where each one goes

All of it is in `bootstrap/app.go`. The import, with the other module imports:

```go
import (
	imports "github.com/hyz-is/arandu-import"
)
```

The construction, in `Build`, after the session store, the queue and the disk
exist and before `k.Register`:

```go
	importsModule, err := imports.New(imports.Config{
		Definitions: []imports.Definition{platesDefinition},
		Queue:       queue,   // hesape/queue's Queue
		Files:       disk,    // the Disk the application keeps files on: a private R2 bucket
		Prefix:      "/painel/importacoes",
		Paths:       imports.PortuguesePaths,
		Activity:    activity.Logger(),
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

The registration, inside the `k.Register(...)` call already there:

```go
		importsModule,
```

The module registers its own jobs and its daily clean: the framework reads
`RegisterJobHandlers` and `Schedule` from a registered module, so the
application's worker and scheduler need nothing else. The worker has to run on
the queue the module pushes to (`Config.QueueName`, `default` unless set).

## A Definition

```go
imports.Definition{
	Key: "plates", Label: "Allowed plates",
	Columns: []imports.Column{
		{Key: "plate", Label: "Plate", Required: true, Headers: []string{"Plates"}, Example: "ABC1D23"},
		{Key: "note", Label: "Note"},
	},
	MaxRows:      5000,
	ImportAction: "whitelist.manage",
	ExportAction: "whitelist.list",
	Validate: func(ctx context.Context, g security.Grant, row imports.Row) (imports.Row, error) {
		// Normalize and check; imports.Refuse("reason") turns the row away.
		return row, nil
	},
	Write: func(ctx context.Context, g security.Grant, row imports.Row) (imports.Outcome, error) {
		// An upsert, with the Grant of whoever confirmed. The queue delivers at
		// least once, so writing a row twice must land in the same place.
		return imports.Created, nil
	},
	Export: &imports.Export{
		Each: func(ctx context.Context, g security.Grant, emit func(imports.Row) error) error {
			return nil // call emit once per record, read with g
		},
	},
}
```

`Validate` writes nothing: it is run on every row to draw the preview and again
by the job. `Write` is the application's own storage. An error made with
`imports.Refuse` refuses one row and the import goes on; any other error stops
the import, and the run says it failed.

## Then run the migrations

```bash
aru migrate
```

Not optional, and not automatic. The package owns the `import_runs` table, which
is why its `arandu.mod.toml` declares `migrations = true`. `aru migrate` is a
step in the pipeline and never a call at the start of the process.

**Symptom to recognise:** `no such table`, or `relation does not exist`, from a
route that authorized correctly. The migration has not run.

## Publish the views

```bash
aru vendor:publish --tag=view --apply
aru view:build
```

and import `storage/framework/views/modules/imports` for its side effect. The
files land under `resources/views/modules/imports/` and are the application's
from then on: its layout, its page head, its words.

## Configuration

| field | required | meaning |
| --- | --- | --- |
| `Definitions` | yes | the kinds of import and export. At least one |
| `Queue` | yes | where the jobs are pushed |
| `Files` | yes | the Disk for the uploads, the reports and the exports |
| `Prefix` | no | where the routes are mounted. Defaults to `/imports` |
| `Paths` | no | the segments under the prefix. `PortuguesePaths` is shipped |
| `Scope` | no | the subject acting in the space the person selected (an application with several spaces per person) |
| `Activity` | no | the activity log; the start and the end of every run, with counts |
| `MaxFileBytes` | no | the largest upload. Defaults to 10 MB, refused above 50 MB |
| `RetentionDays` | no | how long the files are kept. Defaults to 7, refused above 90 |
| `OnChange` | no | told after every progress write, to tell a live table |

There is no tenant to pass: every route needs a session, and the tenant of every
statement is the one on the Grant.

## Routes

| method | path | name |
| --- | --- | --- |
| `GET` | `/imports` | `imports.index` |
| `GET` | `/imports/{definition}` | `imports.new` |
| `POST` | `/imports/{definition}` | `imports.upload` |
| `GET` | `/imports/{definition}/template/{format}` | `imports.template` |
| `POST` | `/imports/{definition}/export` | `imports.export` |
| `GET` | `/imports/{definition}/runs/{id}` | `imports.show` |
| `GET` | `/imports/{definition}/runs/{id}/progress` | `imports.progress` |
| `POST` | `/imports/{definition}/runs/{id}/confirm` | `imports.confirm` |
| `POST` | `/imports/{definition}/runs/{id}/discard` | `imports.discard` |
| `GET` | `/imports/{definition}/runs/{id}/refused` | `imports.refused` |
| `GET` | `/imports/{definition}/runs/{id}/file` | `imports.file` |

Every route sits behind `RequireAuth`: a request with no session is sent to
`/auth/login`. Build addresses from `Module.URLs()`, never by writing a path a
second time.

## Who may do what

The policy denies by default. It answers about the actions the definitions
name, which are the application's own: a subject that holds `ImportAction` may
import with that definition and see its runs; one that holds `ExportAction` may
export. An action no definition names is refused, and so is a subject that does
not hold the one it names. There is nothing to open inside the package.

## What the answers mean

| status | what happened |
| --- | --- |
| `303` to `/auth/login` | there is no session on the request |
| `403` | the policy refused: the subject does not hold the action |
| `404` | no such definition, or no run with that id in this tenant |
| `303` back to the page | the file was rejected as a whole; the message is in the flash |

## The files

The upload, the report of the refused rows and the export go to `Config.Files`
under keys the server generates, never to the server's own disk. Reading one is
authorized and written to the activity log. The daily clean deletes the files of
every run past `RetentionDays`; the run stays in the history with its counts.

## Reporting a problem

Issues and pull requests go to `github.com/hyz-is/arandu-import`, which belongs
to `hyz-is`. A vulnerability goes to the private advisory form named in that
repository's `SECURITY.md`, and never into an issue.

MIT licensed. Copyright HYZIS.
