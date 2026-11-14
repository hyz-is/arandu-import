//go:build kyse

package imports

import (
	imports "github.com/hyz-is/arandu-import"
)

@go
// ShowData is what the handler hands this page, and what its state is drawn
// from: the state partial is included with the same data, and the progress
// route answers it alone while the run goes.
type ShowData = imports.ShowPageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-4xl min-w-0 px-4 py-8 sm:px-6 lg:px-8">
	<p class="text-sm"><a class="text-muted-foreground hover:underline" href="{{ .BackURL }}">{{ .Labels.T("control.back") }}</a></p>

	<header class="mt-3 min-w-0">
		<p class="text-muted-foreground text-sm">{{ .Run.DefinitionLabel }} · {{ .Run.KindLabel }}</p>
		<h1 class="text-2xl font-semibold tracking-tight break-words">{{ .Run.FileName }}</h1>
		<p class="text-muted-foreground mt-1 text-sm">{{ .StartedText() }}</p>
	</header>

	<div class="mt-6">
		@include('modules.imports.state')
	</div>
</section>
@endsection
