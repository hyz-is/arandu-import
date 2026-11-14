<p align="center">
  <img src=".github/logo.svg" alt="Arandu" width="180">
</p>

# Arandu Import

An Arandu package that imports and exports records from files.

The person attaches a CSV or an XLSX, sees what the application understood of
it, confirms, and watches the import run in the background. A report says how
many rows went in, how many were updated and which were refused, with the row
number and the reason, and the refused rows download as a CSV to correct and
send again. The same screens export what is stored, as CSV or XLSX.

It is dynamic. The application registers a `Definition` for each kind of import
— its columns, how a row is checked and where it is written — and the package
draws the screens, reads the file, runs the job and keeps the history. Nothing in
it knows what a plate, a customer or a product is.

- **CSV** with `;`, `,` or a tab, in UTF-8, UTF-16 or Latin-1/Windows-1252, with
  or without a heading row. **XLSX**, first sheet.
- **No cgo, no spreadsheet dependency.** Reading and writing XLSX is the standard
  library's `archive/zip` and `encoding/xml`, so the binary of whoever installs
  it stays static.
- **Preview before anything is written**, and a job on the queue with progress
  the screen follows.
- **Files on the application's Disk** (in production a private bucket), under
  keys the server generates, read back only through an authorized route, and
  deleted by a daily clean after `RetentionDays`.
- **Tenant from the Grant**, activity log entries at the start and the end of
  every run with counts and never the content of a row.

## Install

```bash
go get github.com/hyz-is/arandu-import
```

## Wire it

An Arandu application registers a module explicitly. There is no service
provider, no container and no discovery, so these are the lines to paste into
`bootstrap/app.go` and there are no others.

The import, with the other module imports:

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
		Queue:       queue,
		Files:       disk,
		Activity:    activity.Logger(),
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

And the registration, inside the `k.Register(...)` call already there:

```go
		importsModule,
```

The module hands the worker its two jobs (`RegisterJobHandlers`) and the
scheduler its daily clean (`Schedule`); the framework reads both from a
registered module. Then, once, before the application serves:

```bash
aru migrate
```

This package owns a table, which is why the migration step is not optional and
why `arandu.mod.toml` says `migrations = true`.

## A Definition

```go
imports.Definition{
	Key: "plates", Label: "Allowed plates",
	Description: "The plates you allow.",
	Columns: []imports.Column{
		{Key: "plate", Label: "Plate", Required: true, Headers: []string{"Plates"}, Example: "ABC1D23"},
		{Key: "note", Label: "Note"},
	},
	MaxRows:      5000,
	ImportAction: "whitelist.manage",
	ExportAction: "whitelist.list",
	Validate: func(ctx context.Context, g security.Grant, row imports.Row) (imports.Row, error) {
		// Normalize and check. imports.Refuse("reason") turns the row away.
		return row, nil
	},
	Write: func(ctx context.Context, g security.Grant, row imports.Row) (imports.Outcome, error) {
		// Store it, with the Grant of whoever confirmed. An upsert: the queue
		// delivers a job at least once.
		return imports.Created, nil
	},
	Finish: func(ctx context.Context, g security.Grant, s imports.Summary) error { return nil },
	Export: &imports.Export{
		Each: func(ctx context.Context, g security.Grant, emit func(imports.Row) error) error {
			return nil // call emit once per record, read with g
		},
	},
}
```

A file is read by its headings, ignoring case, accents, spaces and marks, so
`Placa`, `PLACAS` and ` placa ` are one column; a column named in `Headers`
answers to those too. A file with no heading is read in the order of `Columns`.
A file that lacks a required column, holds more than `MaxRows` rows or is not a
table is refused whole, with the reason.

`Validate` writes nothing: it is called on every row to draw the preview and
again by the job. `Write` is the application's own storage. An error made with
`imports.Refuse` refuses one row and the import goes on; any other error stops
the import and the run says it failed. `Finish` runs once at the end, for what
depends on the whole file.

The policy asks about `ImportAction` and `ExportAction`, which are the
application's own actions, given to subjects by its permission module. A subject
that holds neither sees nothing.

## Publish the views

This package carries the markup of its own pages and hands it over instead of
rendering it from the inside, because a page you cannot edit is a page that says
the wrong thing in your product.

```bash
aru vendor:publish --tag=view
aru vendor:publish --tag=view --apply
```

Nothing is written without `--apply`. The files land under
`resources/views/modules/imports/`, and from that point they are yours: change
the layout they extend, put the application's page head on them, translate them.
Two steps are left to you. Compile what was written:

```bash
aru view:build
```

and import the directory it wrote into, with the other imports:

```go
	_ "your/module/path/storage/framework/views/modules/imports"
```

Without that import the module refuses to boot, naming the view, the command and
the import. No script and no style is inline: the drop area is Kyse's
`file-upload` behaviour and the progress follows with htmx, both served from
`'self'`.

## Configuration

| field | required | meaning |
| --- | --- | --- |
| `Definitions` | yes | the kinds of import and export. At least one |
| `Queue` | yes | where the jobs are pushed |
| `Files` | yes | the Disk for the uploads, the reports and the exports |
| `Prefix` | no | where the routes are mounted. Defaults to `/imports` |
| `Paths` | no | the segments under the prefix. `PortuguesePaths` is shipped |
| `Middleware` | no | wraps every route, after `RequireAuth` |
| `Activity` | no | the activity log: the start and the end of every run, with counts |
| `OnChange` | no | told after every progress write, to tell a live table |
| `MaxFileBytes` | no | the largest upload. Defaults to 10 MB, refused above 50 MB |
| `RetentionDays` | no | how long the files are kept. Defaults to 7, refused above 90 |
| `CleanSpec` | no | the cron of the clean. Defaults to `20 3 * * *`, `-` schedules none |
| `Chrome`, `Locale`, `Translator`, `TitleSeparator`, `Location` | no | the application's frame, language, words and zone |

`New` returns an error rather than starting half-wired, so a setting that cannot
work fails where it is written instead of on the first request that needed it.

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

Every one of them sits behind `RequireAuth`: a visitor with no session is sent to
`/auth/login`, and the handler of a request that has one reads who is asking with
`ctx.User()`. A handler returns the error the service returned, and the router
answers it: a policy refusal is 403, a run that is not there is 404, and a
rejected file goes back where it came from with the message in the flash.
`Module.URLs()` builds every address.

## The data path

`Run` embeds `model.Model`, and `runTable`, beside it in `model.go`, declares its
table once. `aru model:build` generates `RunQuery.go` from it: `Runs(db)`, which
starts a `*RunQuery`, and `RunCollection`. The file is committed and never
edited by hand; `aru model:build --check` fails when it is out of date.

`Service` owns `*data.DB` and follows
`validate -> security.Authorize -> Grant -> Model terminal`; handlers never hold
the database or start a query. The job runs under the Grant that confirmed the
run: the queue carries its tenant and its action, so `Write` and `Export.Each`
receive a Grant of the same tenant and nothing they read comes from anywhere
else.

A run keeps counts and never the content of a row. The first refused rows are on
the run for the screen, and all of them are in a CSV on the Disk until the clean.

## Layout

```
module.go         registration and routes
config.go         what the application passes in, and the route table
definition.go     what the application states for each kind of import
model.go          the run, its table, its own rules, and what it may answer with
RunQuery.go       the query aru model:build generates from model.go
policy.go         who may do what
service.go        the use cases, and Model access after authorization
job.go            the two jobs the queue runs
clean.go          the daily clean of the files
grid.go, xlsxread.go, writer.go, mapping.go   reading and writing the files
handlers.go, pages.go, screens.go             the routes and what the views draw
views.go          the files the application takes ownership of
```

## What is already correct, and has to stay that way

**The policy denies everything it was not told about.** An action no Definition
names is refused, and so is a subject that does not hold the one it names. The
Service calls `security.Authorize` before its first `Runs(db)` reach, and every
Model terminal requires the Grant that call produced. Looking at a run is
authorized under a read-only form of the action, so a bug on that path cannot
start an import.

**The tenant comes from `data.Tenant(g)`.** Never from the path, the body, the
query string or a header.

**Who is asking comes from the guard.** `RequireAuth` loads the session once and
puts the subject on the request; nothing in the module loads it again.

**`arandu.mod.toml` declares what the package does** — network, filesystem, exec,
migrations — and the suite compares the declaration against what the code
*calls*. The package writes no file of its own: everything goes through the
Disk the application passes in.

## Tests

```bash
go test -race ./...
```

The denial suite constructs the Service with a nil database, so even building
`Runs(nil)` would panic. The feature suite drives the whole flow over a real
SQLite database: every delimiter and encoding, a real XLSX, the header in
Portuguese, a refused row, the job and its progress, the isolation between
tenants, the clean and the export.

## Licence

MIT. See [LICENSE.md](LICENSE.md). Copyright HYZIS.
