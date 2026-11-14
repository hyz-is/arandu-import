//go:build kyse

package imports

import (
	"github.com/arandu-io/kyse/components"

	imports "github.com/hyz-is/arandu-import"
)

@go
// IndexData is what the handler hands this page: the overview of what the
// subject may import or export, or the page of one definition.
//
// It is an alias rather than a struct declared here, so the shape is written
// once, beside the code that fills it.
type IndexData = imports.IndexPageData

// runsTable is the history, paged, filtered, searched and ordered by the
// server. Each row opens the run.
func runsTable(data IndexData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, r := range data.Rows {
		rows = append(rows, components.TableRow{Key: r.ID, Cells: []components.TableCell{
			{HTML: components.Link(components.LinkProps{Label: r.FileName, URL: r.URL, Variant: "hover"})},
			{Text: r.DefinitionLabel + " · " + r.KindLabel},
			{HTML: components.Badge(components.BadgeProps{Label: r.Status, Variant: r.StatusVariant})},
			{Text: r.Result},
			{Text: r.When},
		}})
	}
	return components.DataTableProps{
		ID: "import-history", Label: data.Labels.T("screen.history_title"), URL: data.URL,
		State: data.TableState(), Paginator: data.Pager, FullPager: true, HeaderMenu: true,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Searchable: true, SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.history_title")),
		Filters: []components.DataTableFilter{
			{Key: "status", Label: data.Labels.T("field.status"), Options: data.StatusOptions},
		},
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.name"), Key: "file_name", Sortable: true, Primary: true},
			{Label: data.Labels.T("field.definition"), Key: "definition", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
			{Label: data.Labels.T("field.result"), Hideable: true},
			{Label: data.Labels.T("field.when"), Key: "created_at", Sortable: true},
		},
		Rows: rows,
		Empty: components.EmptyProps{
			Title: data.Labels.T("screen.history_empty"), Message: data.Labels.T("screen.history_empty_lead"),
		},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-5xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	@if(.Overview())
		<header>
			<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.index_title") }}</h1>
			<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.index_lead") }}</p>
		</header>
		@if(len(.Definitions) == 0)
			<div class="mt-6">{!! components.Empty(components.EmptyProps{Title: .Labels.T("screen.overview_empty"), Message: .Labels.T("screen.overview_empty_lead")}) !!}</div>
		@else
			<ul class="mt-6 grid gap-4 sm:grid-cols-2">
				@foreach(.Definitions as def)
					<li class="card min-w-0 p-5">
						<h2 class="text-base font-semibold">{{ def.Label }}</h2>
						@if(def.Description != "")
							<p class="text-muted-foreground mt-1 text-sm">{{ def.Description }}</p>
						@endif
						<p class="mt-4"><a class="btn" data-variant="outline" href="{{ def.URL }}">{{ .Labels.T("screen.open") }}</a></p>
					</li>
				@endforeach
			</ul>
		@endif
	@else
		<header>
			<p class="text-sm"><a class="text-muted-foreground hover:underline" href="{{ .BackURL() }}">{{ .Labels.T("control.back") }}</a></p>
			<h1 class="mt-3 text-2xl font-semibold tracking-tight">{{ .Definition.Label }}</h1>
			@if(.Definition.Description != "")
				<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Definition.Description }}</p>
			@endif
		</header>

		@if(.CanImport)
			<form id="import-upload" class="card mt-6 grid gap-4 p-5" method="post" action="{{ .UploadURL }}" enctype="multipart/form-data">
				@csrf
				<div>
					<h2 class="text-base font-semibold">{{ .Labels.T("screen.upload_title") }}</h2>
					<p class="text-muted-foreground mt-1 text-sm">{{ .Labels.T("screen.upload_lead") }}</p>
				</div>
				@if(.HasErrors())
					<ul class="text-destructive grid gap-1 text-sm" role="alert">
						@foreach(.All() as line)
							<li>{{ line }}</li>
						@endforeach
					</ul>
				@endif
				{!! components.FileUpload(components.FileUploadProps{Name: "file", ID: "import-file", Label: .Labels.T("field.file"), Accept: .Accept, Prompt: .Labels.T("screen.upload_prompt"), Hint: .UploadHint(), UploadURL: .UploadURL, Required: true, Page: .Form()}) !!}
				<div class="flex flex-wrap items-center gap-2">
					{!! components.Button(components.ButtonProps{Label: .Labels.T("control.upload"), Type: "submit"}) !!}
					<a class="btn" data-variant="outline" href="{{ .TemplateXLSX }}" hx-boost="false">{{ .Labels.T("control.template_xlsx") }}</a>
					<a class="btn" data-variant="outline" href="{{ .TemplateCSV }}" hx-boost="false">{{ .Labels.T("control.template_csv") }}</a>
				</div>
			</form>

			<section class="card mt-6 p-5" aria-labelledby="import-columns">
				<h2 id="import-columns" class="text-base font-semibold">{{ .Labels.T("screen.columns_title") }}</h2>
				<p class="text-muted-foreground mt-1 text-sm">{{ .Labels.T("screen.model_lead") }}</p>
				<ul class="mt-3 grid gap-2 text-sm">
					@foreach(.Columns as column)
						<li>
							<span class="font-medium">{{ column.Label }}</span>
							<span class="text-muted-foreground">· @if(column.Required){{ .Labels.T("screen.required") }}@else{{ .Labels.T("screen.optional") }}@endif</span>
							@if(column.Hint != "")
								<span class="text-muted-foreground block">{{ column.Hint }}</span>
							@endif
						</li>
					@endforeach
				</ul>
			</section>
		@endif

		@if(.CanExport)
			<section class="card mt-6 p-5" aria-labelledby="import-export">
				<h2 id="import-export" class="text-base font-semibold">{{ .Labels.T("screen.export_title") }}</h2>
				<p class="text-muted-foreground mt-1 text-sm">{{ .ExportLead() }}</p>
				<div class="mt-3 flex flex-wrap gap-2">
					<form method="post" action="{{ .ExportURL }}">
						@csrf
						<input type="hidden" name="format" value="xlsx">
						{!! components.Button(components.ButtonProps{Label: .Labels.T("control.export_xlsx"), Variant: "outline", Type: "submit"}) !!}
					</form>
					<form method="post" action="{{ .ExportURL }}">
						@csrf
						<input type="hidden" name="format" value="csv">
						{!! components.Button(components.ButtonProps{Label: .Labels.T("control.export_csv"), Variant: "outline", Type: "submit"}) !!}
					</form>
				</div>
			</section>
		@endif
	@endif

	<section class="mt-8 min-w-0" aria-labelledby="import-history-title">
		<h2 id="import-history-title" class="mb-3 text-base font-semibold">{{ .Labels.T("screen.history_title") }}</h2>
		{!! components.DataTable(runsTable(.)) !!}
	</section>
</section>
@endsection
