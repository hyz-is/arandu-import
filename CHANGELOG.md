# Changelog

Everything worth knowing about a release of Arandu Import is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

## [0.1.0] - 2026-11-14

### Added

- Import and export of records from files, dynamic: the application registers a
  `Definition` for each kind (its columns, how a row is checked, where it is
  written, and optionally how it is exported), and the package draws the screens,
  reads the file, runs the job and keeps the history.
- Reading of CSV with `;`, `,` or a tab, in UTF-8, UTF-16 or Latin-1/Windows-1252,
  with or without a heading row, and of the first sheet of an XLSX, with the
  standard library alone: no cgo and no spreadsheet dependency, so the binary of
  an application stays static. Headings are matched ignoring case, accents,
  spaces and marks, and a column may name other headings it answers to.
- The flow: attach the file (the drop area is Kyse's `file-upload` behaviour),
  download the template in CSV or XLSX, see a preview with the recognised
  columns and every refused row, confirm, follow the progress while the job runs
  on the queue, read the report and download the refused rows as a CSV, and look
  at the history of the space. Exports are written in the background and the run
  says when the file is ready.
- The two jobs, `imports.import` and `imports.export`, registered through
  `RegisterJobHandlers`. `Write` and `Export.Each` receive the Grant of whoever
  confirmed, carried by the job.
- One table, `import_runs` (migration `20261113_0001_create_import_runs`), that
  keeps counts and never the content of a row.
- Files on the application's `Disk`, under keys the server generates, read back
  only through an authorized route that is written to the activity log; the
  daily clean (`Schedule`, `CleanAction`) deletes them after `RetentionDays`.
- The start and the end of every run in the activity log, with counts.
- The policy answers about the actions the definitions name, in a write form and
  a read-only form (`ViewOf`), so listing a run or downloading a report cannot
  start an import. The package declares one action of its own, `CleanAction`,
  issued to the scheduler.
- `pt-BR` and `en` catalogues and `PortuguesePaths`.
