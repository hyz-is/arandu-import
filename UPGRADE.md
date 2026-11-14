# Upgrade Guide

## Unreleased

## v0.1.0

First release. Nothing to upgrade from.

Run `aru migrate` once: the package owns the `import_runs` table. Publish the
views with `aru vendor:publish --tag=view --apply`, compile them with
`aru view:build` and import `storage/framework/views/modules/imports`.
