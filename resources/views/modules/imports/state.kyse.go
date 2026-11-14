//go:build kyse

package imports

import (
	"github.com/arandu-io/kyse/components"

	imports "github.com/hyz-is/arandu-import"
)

@go
// StateData is what the page and the progress route hand this partial.
type StateData = imports.ShowPageData

// lineTable lays rows out as a table that holds all of them: a preview or a
// list of refusals is never longer than the module keeps.
func lineTable(data StateData, id, label string, lines []imports.LineRow, reasons bool) components.DataTableProps {
	columns := []components.TableColumn{{Label: data.Labels.T("field.line")}}
	for _, head := range data.Heads {
		columns = append(columns, components.TableColumn{Label: head})
	}
	if reasons {
		columns = append(columns, components.TableColumn{Label: data.Labels.T("field.reason")})
	}
	rows := make([]components.TableRow, 0, len(lines))
	for _, line := range lines {
		cells := []components.TableCell{{Text: itoa(line.Number)}}
		for _, cell := range line.Cells {
			cells = append(cells, components.TableCell{Text: cell})
		}
		if reasons {
			cells = append(cells, components.TableCell{Text: line.Reason})
		}
		rows = append(rows, components.TableRow{Key: itoa(line.Number), Cells: cells})
	}
	return components.DataTableProps{
		ID: id, Label: label, URL: data.Run.URL, Columns: columns, Rows: rows, Complete: true, Lines: data.TableLines(),
		Empty: components.EmptyProps{Title: data.Labels.T("table.no_result")},
	}
}

func itoa(n int) string { return imports.Itoa(n) }
@endgo

<div id="import-state" class="grid min-w-0 gap-6" aria-live="polite"
	@if(.Polling)
		hx-get="{{ .ProgressURL }}" hx-trigger="every 1s" hx-target="this" hx-swap="outerHTML"
	@endif
>
	@if(.Run.StatusKey == "uploaded")
		<section class="card grid min-w-0 gap-5 p-5" aria-labelledby="preview-title">
			<div>
				<h2 id="preview-title" class="text-base font-semibold">{{ .Labels.T("screen.preview_title") }}</h2>
				<p class="text-muted-foreground mt-1 text-sm">{{ .Labels.T("screen.preview_lead") }}</p>
			</div>
			<dl class="grid grid-cols-3 gap-3 text-sm">
				<div><dt class="text-muted-foreground">{{ .Labels.T("screen.rows_total") }}</dt><dd class="text-xl font-semibold">{{ .Run.Total }}</dd></div>
				<div><dt class="text-muted-foreground">{{ .Labels.T("screen.rows_ok") }}</dt><dd class="text-xl font-semibold">{{ .Valid }}</dd></div>
				<div><dt class="text-muted-foreground">{{ .Labels.T("screen.rows_bad") }}</dt><dd class="text-xl font-semibold">{{ .Invalid }}</dd></div>
			</dl>
			<div class="grid gap-2 text-sm">
				<h3 class="font-semibold">{{ .Labels.T("screen.recognized") }}</h3>
				<p class="text-muted-foreground">@if(.Header){{ .Labels.T("screen.preview_header_yes") }}@else{{ .Labels.T("screen.preview_header_no") }}@endif</p>
				<ul class="grid gap-1">
					@foreach(.Mapped as column)
						<li>
							<span class="font-medium">{{ column.Label }}</span>
							@if(column.Heading != "")
								<span class="text-muted-foreground">← {{ column.Heading }}</span>
							@elseif(!column.Found)
								<span class="text-destructive">· {{ .Labels.T("screen.not_found") }}</span>
							@endif
						</li>
					@endforeach
				</ul>
				@if(.Ignored != "")
					<p class="text-muted-foreground">{{ .IgnoredText() }}</p>
				@endif
			</div>
			<div class="grid min-w-0 gap-2">
				<h3 class="text-sm font-semibold">{{ .Labels.T("screen.first_rows") }}</h3>
				{!! components.DataTable(lineTable(., "import-sample", .Labels.T("screen.first_rows"), .Sample, true)) !!}
			</div>
			@if(.Invalid > 0)
				<div class="grid min-w-0 gap-2">
					<h3 class="text-sm font-semibold">{{ .Labels.T("screen.problems_title") }}</h3>
					{!! components.DataTable(lineTable(., "import-problems", .Labels.T("screen.problems_title"), .Problems, true)) !!}
					@if(.MoreProblem > 0)
						<p class="text-muted-foreground text-sm">{{ .MoreText(false) }}</p>
					@endif
				</div>
			@endif
			@if(.CanConfirm)
				<div class="grid gap-3">
					@if(.Valid > 0)
						<p class="text-muted-foreground text-sm">{{ .Labels.T("screen.confirm_lead") }} {{ .KeepNote() }}</p>
					@else
						<p class="text-destructive text-sm">{{ .Labels.T("screen.confirm_none") }}</p>
					@endif
					<div class="flex flex-wrap items-center gap-2">
						@if(.Valid > 0)
							<form method="post" action="{{ .ConfirmURL }}">
								@csrf
								{!! components.Button(components.ButtonProps{Label: .Labels.T("control.confirm"), Type: "submit"}) !!}
							</form>
						@endif
						<form method="post" action="{{ .DiscardURL }}">
							@csrf
							{!! components.Button(components.ButtonProps{Label: .Labels.T("control.discard"), Variant: "outline", Type: "submit"}) !!}
						</form>
					</div>
				</div>
			@endif
		</section>
	@elseif(.Run.Active)
		<section class="card grid gap-4 p-5" aria-labelledby="run-title">
			<div>
				<h2 id="run-title" class="text-base font-semibold">{{ .ProgressTitle() }}</h2>
				<p class="text-muted-foreground mt-1 text-sm">{{ .ProgressLead() }}</p>
			</div>
			{!! components.Progress(components.ProgressProps{Label: .ProgressTitle(), Value: .Run.Percent, Indeterminate: .Indeterminate || .Run.StatusKey == "queued"}) !!}
		</section>
	@elseif(.Run.StatusKey == "failed")
		<section class="grid gap-4">
			{!! components.Alert(components.AlertProps{Variant: "destructive", Title: .Labels.T("screen.failed_title"), Message: .Failure}) !!}
			@if(.Run.IsImport)
				@include('modules.imports.counts')
			@endif
			<p><a class="btn" data-variant="outline" href="{{ .BackURL }}">{{ .Labels.T("control.new_import") }}</a></p>
		</section>
	@elseif(.Run.StatusKey == "discarded")
		<section class="card grid gap-3 p-5">
			<h2 class="text-base font-semibold">{{ .Labels.T("screen.discarded_title") }}</h2>
			<p class="text-muted-foreground text-sm">{{ .Labels.T("screen.discarded_lead") }}</p>
			<p><a class="btn" data-variant="outline" href="{{ .BackURL }}">{{ .Labels.T("control.new_import") }}</a></p>
		</section>
	@elseif(.Run.IsExport)
		<section class="card grid gap-3 p-5" aria-labelledby="export-done">
			<h2 id="export-done" class="text-base font-semibold">{{ .Labels.T("screen.done_export_title") }}</h2>
			<p class="text-muted-foreground text-sm">{{ .ExportDoneLead() }}</p>
			<div class="flex flex-wrap items-center gap-2">
				@if(.HasFile)
					<a class="btn" href="{{ .FileURL }}" hx-boost="false">{{ .Labels.T("control.download_file") }} ({{ .Run.Format }}, {{ .Run.Size }})</a>
				@else
					<p class="text-muted-foreground text-sm">{{ .Labels.T("screen.files_gone") }}</p>
				@endif
				<a class="btn" data-variant="outline" href="{{ .BackURL }}">{{ .Labels.T("control.back") }}</a>
			</div>
		</section>
	@else
		<section class="card grid min-w-0 gap-5 p-5" aria-labelledby="import-done">
			<h2 id="import-done" class="text-base font-semibold">{{ .Labels.T("screen.done_title") }}</h2>
			@include('modules.imports.counts')
			@if(.Run.Refused > 0)
				<div class="grid min-w-0 gap-2">
					<div>
						<h3 class="text-sm font-semibold">{{ .Labels.T("screen.refused_title") }}</h3>
						<p class="text-muted-foreground text-sm">{{ .Labels.T("screen.refused_lead") }}</p>
					</div>
					{!! components.DataTable(lineTable(., "import-refused", .Labels.T("screen.refused_title"), .Problems, true)) !!}
					@if(.MoreProblem > 0)
						<p class="text-muted-foreground text-sm">{{ .MoreText(true) }}</p>
					@endif
				</div>
			@endif
			<div class="flex flex-wrap items-center gap-2">
				@if(.HasReport)
					<a class="btn" href="{{ .RefusedURL }}" hx-boost="false">{{ .Labels.T("control.download_refused") }}</a>
				@endif
				<a class="btn" data-variant="outline" href="{{ .BackURL }}">{{ .Labels.T("control.new_import") }}</a>
			</div>
		</section>
	@endif
</div>
