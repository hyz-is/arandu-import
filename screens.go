package imports

import (
	"strings"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
)

// runRow is a run as a screen shows it, with the name of its definition.
func (m *Module) runRow(sc Screen, run *Run) RunRow {
	row := sc.runRow(run)
	row.DefinitionLabel = m.service.labelOf(run.Definition)
	return row
}

// indexPage is what the index page, and the page of one definition, is drawn
// from.
func (m *Module) indexPage(ctx *fhttp.Context, sc Screen, who security.Subject, def *Definition, p params, page Page) IndexPageData {
	u := m.URLs()
	rows := make([]RunRow, 0, len(page.Rows))
	for _, run := range page.Rows {
		rows = append(rows, m.runRow(sc, run))
	}
	statuses := make([]string, 0, len(p.statuses))
	for _, s := range p.statuses {
		statuses = append(statuses, string(s))
	}
	listURL := u.Index()
	if def != nil {
		listURL = u.Definition(def.Key)
	}
	data := IndexPageData{
		Screen: sc, Rows: rows, Query: p.search, Statuses: statuses, SortKey: p.sort, SortDir: p.dir, URL: listURL,
		Pager:       pager(len(rows), page.Total, m.cfg.PageSize, page.Page, listURL, p.values()),
		CurrentPage: page.Page, Pages: page.Pages, Total: page.Total, PerPage: m.cfg.PageSize,
		StatusOptions: sc.StatusOptions(), Retention: m.cfg.RetentionDays, Accept: strings.Join(AcceptedExtensions(), ","),
		MaxMB: megabytes(m.cfg.MaxFileBytes),
	}
	if def == nil {
		for _, d := range m.cfg.Definitions {
			if v := m.definitionView(ctx, who, d); v.CanImport || v.CanExport {
				data.Definitions = append(data.Definitions, v)
			}
		}
		return data
	}
	v := m.definitionView(ctx, who, *def)
	data.Definition = &v
	data.CanImport, data.CanExport = v.CanImport, v.CanExport
	data.MaxRows = def.MaxRows
	data.UploadURL = u.Definition(def.Key)
	data.TemplateCSV, data.TemplateXLSX = u.Template(def.Key, FormatCSV), u.Template(def.Key, FormatXLSX)
	data.ExportURL = u.Export(def.Key)
	for _, c := range def.Columns {
		mark := sc.Labels.T("screen.optional")
		if c.Required {
			mark = sc.Labels.T("screen.required")
		}
		data.Columns = append(data.Columns, ColumnView{Label: c.Label, Hint: c.Hint, Required: c.Required, Mark: mark})
	}
	return data
}

// showPage is what the page of a run, and the fragment of its state, are drawn
// from.
func (m *Module) showPage(ctx *fhttp.Context, sc Screen, who security.Subject, run *Run) ShowPageData {
	u := m.URLs()
	def, _ := m.cfg.definition(run.Definition)
	data := ShowPageData{
		Screen: sc, Run: m.runRow(sc, run), Definition: m.definitionView(ctx, who, def),
		BackURL:     u.Definition(run.Definition),
		ProgressURL: u.Progress(run.Definition, run.ID), Polling: run.Current().Active(),
		ConfirmURL: u.Confirm(run.Definition, run.ID), DiscardURL: u.Discard(run.Definition, run.ID),
		RefusedURL: u.Refused(run.Definition, run.ID), FileURL: u.File(run.Definition, run.ID),
		HasFile: run.HasFile() && run.IsExport(), HasReport: run.HasReport(),
		Failure: sc.Labels.FailureText(run.Failure), Retention: m.cfg.RetentionDays,
		Indeterminate: run.IsExport() || run.Total == 0,
	}
	data.CanConfirm = run.CanConfirm() && run.HasFile() && data.Definition.CanImport
	for _, c := range def.Columns {
		data.Heads = append(data.Heads, c.Label)
	}

	switch {
	case run.IsImport() && run.Current() == StatusUploaded:
		p := decodePreview(run.Preview)
		data.Header, data.Valid, data.Invalid = p.Header, p.Valid, p.Invalid
		for _, c := range p.Columns {
			data.Mapped = append(data.Mapped, MappedRow{Label: c.Label, Heading: c.Heading, Required: c.Required, Found: c.Heading != "" || !p.Header})
		}
		data.Ignored = strings.Join(p.Ignored, ", ")
		data.Sample = lineRows(def, p.Sample)
		data.Problems = lineRows(def, p.Problems)
		data.MoreProblem = p.Invalid - len(p.Problems)
	case run.IsImport() && run.Refused > 0:
		lines := decodeLines(run.Sample)
		data.Problems = lineRows(def, lines)
		data.MoreProblem = int(run.Refused) - len(lines)
	}
	if data.MoreProblem < 0 {
		data.MoreProblem = 0
	}
	return data
}

// lineRows lays the rows of a preview out as the definition's columns, in order.
func lineRows(def Definition, lines []Line) []LineRow {
	out := make([]LineRow, 0, len(lines))
	for _, l := range lines {
		cells := make([]string, len(def.Columns))
		for i, c := range def.Columns {
			cells[i] = l.Values[c.Key]
		}
		out = append(out, LineRow{Number: l.Number, Cells: cells, Reason: l.Reason})
	}
	return out
}
