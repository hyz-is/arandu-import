//go:build kyse

package imports

import (
	imports "github.com/hyz-is/arandu-import"
)

@go
// CountsData is what the state partial hands this one: the page's own data.
type CountsData = imports.ShowPageData
@endgo

<dl class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
	<div><dt class="text-muted-foreground">{{ .Labels.T("screen.created") }}</dt><dd class="text-xl font-semibold">{{ .Run.Created }}</dd></div>
	<div><dt class="text-muted-foreground">{{ .Labels.T("screen.updated") }}</dt><dd class="text-xl font-semibold">{{ .Run.Updated }}</dd></div>
	<div><dt class="text-muted-foreground">{{ .Labels.T("screen.unchanged") }}</dt><dd class="text-xl font-semibold">{{ .Run.Unchanged }}</dd></div>
	<div><dt class="text-muted-foreground">{{ .Labels.T("screen.refused") }}</dt><dd class="text-xl font-semibold">{{ .Run.Refused }}</dd></div>
</dl>
