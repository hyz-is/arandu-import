# Upgrade Guide

## Unreleased

Nothing stops compiling. The published views draw `ypy-*` attributes where they
drew the attributes of the old library, and the module recognises a request from
the script by `YPY-Request`.

- **An application that already published the views keeps its own**, which
  still carry the old attributes: the progress panel no longer polls and the
  download links are no longer left out of a boosted navigation until the views
  are converted. `aru vendor:publish --tag=view` reports the difference;
  `ypy migrate --write` on the published directory converts it.
- **The panel polls with `ypy-trigger="every 1s"`** and replaces itself
  (`ypy-swap="outerHTML"`), so the answer that no longer carries the trigger is
  what ends the polling, as before.
- **Needs the Framework and Hesape releases that carry YPY**: a request from the
  script is recognised by `YPY-Request` and answered with `YPY-Redirect`.

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
