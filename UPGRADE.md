# Upgrade Guide

## Unreleased

## v0.1.1

Nothing breaks. An application that already published the views keeps its own:
`aru vendor:publish --tag=view` reports the difference. To show the name of the
chosen file, register the `import-file` behaviour the README lists; without it
the area keeps its sentence. The upload is no longer sent when the file is
chosen, so the "Revisar arquivo" button is how it is sent.

## v0.1.0

First release. Nothing to upgrade from.

Run `aru migrate` once: the package owns the `import_runs` table. Publish the
views with `aru vendor:publish --tag=view --apply`, compile them with
`aru view:build` and import `storage/framework/views/modules/imports`.
