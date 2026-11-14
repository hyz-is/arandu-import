package feature_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	imports "github.com/hyz-is/arandu-import"
)

func plateCSV(sep string) []byte {
	return []byte(strings.Join([]string{
		"Placa" + sep + "Nota",
		"ABC1D23" + sep + "Carro da família",
		"qwe-2r45" + sep + "",
		"ABC1234" + sep + "João",
	}, "\r\n") + "\r\n")
}

// latin1 is what an older Excel saves: one byte for each letter.
func latin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}

func TestACSVInEachDelimiterAndEncodingGoesInAndIsRead(t *testing.T) {
	utf16le := func(s string) []byte {
		out := []byte{0xFF, 0xFE}
		for _, u := range utf16.Encode([]rune(s)) {
			out = append(out, byte(u), byte(u>>8))
		}
		return out
	}
	cases := map[string][]byte{
		"semicolon": plateCSV(";"),
		"comma":     plateCSV(","),
		"tab":       plateCSV("\t"),
		"utf8 bom":  append([]byte{0xEF, 0xBB, 0xBF}, plateCSV(";")...),
		"utf16":     utf16le(string(plateCSV(";"))),
		"latin1":    latin1(string(plateCSV(";"))),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := install(t)
			run := w.upload(t, ana, "placas.csv", body)
			if run.Status != string(imports.StatusUploaded) || run.Total != 3 {
				t.Fatalf("status %s total %d, want uploaded with 3 rows", run.Status, run.Total)
			}
			if w.store.writes != 0 {
				t.Fatal("the preview wrote to the application's table")
			}
			done := w.confirmAndRun(t, ana, run.ID)
			if done.Status != string(imports.StatusDone) || done.Created != 3 || done.Refused != 0 {
				t.Fatalf("done = %+v, want 3 created", done)
			}
			got := w.store.get("acme")
			if got["ABC1D23"] != "Carro da família" || len(got) != 3 {
				t.Errorf("stored %v", got)
			}
			if got["ABC1234"] != "João" {
				t.Errorf("the note lost its accents: %q", got["ABC1234"])
			}
		})
	}
}

func TestARealXLSXFromTheTemplateGoesInWithPortugueseHeadings(t *testing.T) {
	w := install(t)
	var template bytes.Buffer
	name, kind, err := w.service.Template(context.Background(), ana, "plates", "xlsx", &template)
	if err != nil {
		t.Fatal(err)
	}
	if name != "modelo-plates.xlsx" || !strings.Contains(kind, "spreadsheetml") {
		t.Errorf("template name %q, type %q", name, kind)
	}
	// The template is itself a workbook the importer reads: one example row.
	run := w.upload(t, ana, name, template.Bytes())
	if run.Total != 1 || run.Format != imports.FormatXLSX {
		t.Fatalf("total %d format %s, want the example row of an xlsx", run.Total, run.Format)
	}

	// Headings in another spelling and order, in an export of the same package.
	body := []byte("OBSERVAÇÃO;PLACAS;Cor\nvisita;ZZZ9Z99;azul\n")
	run = w.upload(t, ana, "x.csv", body)
	done := w.confirmAndRun(t, ana, run.ID)
	if done.Created != 1 || w.store.get("acme")["ZZZ9Z99"] != "visita" {
		t.Errorf("done %+v stored %v", done, w.store.get("acme"))
	}
	preview := imports.NewResource(*run).ToArray()["preview"].(imports.Preview)
	if fmt.Sprint(preview.Ignored) != "[Cor]" || !preview.Header {
		t.Errorf("preview ignored=%v header=%v", preview.Ignored, preview.Header)
	}
}

func TestARowThatIsWrongIsRefusedWithItsLineAndTheReportDownloads(t *testing.T) {
	w := install(t)
	body := []byte("Placa;Nota\nABC1D23;ok\n\nABC;curta\nXYZ9K88;ok\n12345678;longa\n")
	run := w.upload(t, ana, "placas.csv", body)
	preview := imports.NewResource(*run).ToArray()["preview"].(imports.Preview)
	if preview.Valid != 2 || preview.Invalid != 2 {
		t.Fatalf("preview valid %d invalid %d, want 2 and 2", preview.Valid, preview.Invalid)
	}
	if preview.Problems[0].Number != 4 || preview.Problems[0].Reason != "Placa inválida" || preview.Problems[0].Values["plate"] != "ABC" {
		t.Errorf("the first problem = %+v, want line 4 with its reason and value", preview.Problems[0])
	}

	done := w.confirmAndRun(t, ana, run.ID)
	if done.Created != 2 || done.Refused != 2 || done.Processed != 4 {
		t.Fatalf("done = created %d refused %d processed %d", done.Created, done.Refused, done.Processed)
	}
	if len(w.store.get("acme")) != 2 {
		t.Errorf("a refused row was stored: %v", w.store.get("acme"))
	}

	_, file, err := w.service.Open(context.Background(), ana, "plates", run.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Body.Close()
	if file.Name != "recusadas-placas.csv" || !strings.HasPrefix(file.ContentType, "text/csv") {
		t.Errorf("report served as %q (%s)", file.Name, file.ContentType)
	}
	raw, _ := io.ReadAll(file.Body)
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})))
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"Linha", "Placa", "Nota", "Motivo"}, {"4", "ABC", "curta", "Placa inválida"}, {"6", "12345678", "longa", "Placa inválida"}}
	if fmt.Sprint(records) != fmt.Sprint(want) {
		t.Errorf("report = %v, want %v", records, want)
	}
	// The report corrected is a file the importer takes again.
	if _, err := w.service.Upload(context.Background(), ana, "plates", imports.Upload{Name: "recusadas.csv", Body: bytes.NewReader(raw)}); err != nil {
		t.Errorf("the report was not accepted back: %v", err)
	}
}

func TestAnUpdatedPlateKeepsWhatTheLineLeavesSilent(t *testing.T) {
	w := install(t)
	first := w.upload(t, ana, "a.csv", []byte("Placa;Nota\nABC1D23;antiga\nQWE2R45;fixa\n"))
	w.confirmAndRun(t, ana, first.ID)
	second := w.upload(t, ana, "b.csv", []byte("Placa;Nota\nABC1D23;nova\nQWE2R45;\nNEW1A11;\n"))
	done := w.confirmAndRun(t, ana, second.ID)
	if done.Created != 1 || done.Updated != 1 || done.Unchanged != 1 {
		t.Errorf("created %d updated %d unchanged %d, want 1 each", done.Created, done.Updated, done.Unchanged)
	}
	if got := w.store.get("acme"); got["ABC1D23"] != "nova" || got["QWE2R45"] != "fixa" {
		t.Errorf("stored %v", got)
	}
}

func TestAFileRefusedAsAWholeSaysWhy(t *testing.T) {
	w := install(t)
	cases := map[string]struct {
		name string
		body []byte
		want string
	}{
		"empty":       {"a.csv", []byte(""), "vazio"},
		"no column":   {"a.csv", []byte("Nota;Cor\nx;y\n"), "Placa"},
		"pdf":         {"a.pdf", []byte("%PDF"), "CSV ou Excel"},
		"xls":         {"a.xls", []byte("x"), ".xls"},
		"no rows":     {"a.csv", []byte("Placa;Nota\n"), "nenhuma linha"},
		"too many":    {"a.csv", []byte("Placa\n" + strings.Repeat("ABC1D23\n", 201)), "200"},
		"fake sheet":  {"a.xlsx", []byte("not a zip"), "ler este arquivo"},
		"binary text": {"a.csv", []byte("a;b\x00\x01"), "CSV ou Excel"},
	}
	for name, c := range cases {
		_, err := w.service.Upload(context.Background(), ana, "plates", imports.Upload{Name: c.name, Body: bytes.NewReader(c.body)})
		var invalid validation.Errors
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want validation errors", name, err)
			continue
		}
		if msg := strings.Join(invalid.Get("file"), " "); !strings.Contains(msg, c.want) {
			t.Errorf("%s: message %q lacks %q", name, msg, c.want)
		}
	}
	if _, err := w.service.Upload(context.Background(), ana, "plates", imports.Upload{Name: "a.csv", Body: strings.NewReader("Placa\n" + strings.Repeat("A", 11<<20))}); err == nil {
		t.Error("a file over the limit was accepted")
	}
	if n := w.pending(t); n != 0 {
		t.Errorf("%d jobs queued by uploads nobody confirmed", n)
	}
}

func TestTheJobReportsItsProgressAndEndsAt100(t *testing.T) {
	w := install(t)
	var b strings.Builder
	b.WriteString("Placa;Nota\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "ABC%04d;n%d\n", i, i)
	}
	run := w.upload(t, ana, "many.csv", []byte(b.String()))
	queued, err := w.service.Confirm(context.Background(), ana, "plates", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != string(imports.StatusQueued) || w.pending(t) != 1 {
		t.Fatalf("status %s, %d jobs: want queued with one job", queued.Status, w.pending(t))
	}
	// Confirming twice is confirming once.
	if _, err := w.service.Confirm(context.Background(), ana, "plates", run.ID); err != nil || w.pending(t) != 1 {
		t.Fatalf("a second confirmation queued another job (%d) or failed: %v", w.pending(t), err)
	}
	w.work(t)

	seen := w.changes.of(run.ID)
	var last int64 = -1
	sawRunning := false
	for _, c := range seen {
		if c.Processed < last {
			t.Errorf("progress went back from %d to %d", last, c.Processed)
		}
		last = c.Processed
		sawRunning = sawRunning || c.Status == string(imports.StatusRunning) && c.Processed > 0 && c.Processed < 30
	}
	if !sawRunning || seen[len(seen)-1].Status != string(imports.StatusDone) || last != 30 {
		t.Errorf("progress = %+v, want running counts and a final done with 30", seen)
	}
	done, _ := w.service.Find(context.Background(), ana, "plates", run.ID)
	if done.Percent() != 100 || done.Created != 30 {
		t.Errorf("done: percent %d created %d", done.Percent(), done.Created)
	}
}

func TestAWriteThatIsNotARefusalStopsTheImportAndSaysItFailed(t *testing.T) {
	w := install(t)
	w.store.failOn = "QWE2R45"
	run := w.upload(t, ana, "a.csv", []byte("Placa\nABC1D23\nQWE2R45\nXYZ9K88\n"))
	done := w.confirmAndRun(t, ana, run.ID)
	if done.Status != string(imports.StatusFailed) || done.Failure == "" {
		t.Fatalf("status %s failure %q, want failed", done.Status, done.Failure)
	}
	if strings.Contains(done.Failure, "EOF") {
		t.Errorf("the failure carries the raw error: %q", done.Failure)
	}
	if w.pending(t) != 0 {
		t.Error("a failed run was handed back to the queue")
	}
}

func TestAnUploadThrownAwayLosesItsFileAndCannotBeConfirmed(t *testing.T) {
	w := install(t)
	run := w.upload(t, ana, "a.csv", []byte("Placa\nABC1D23\n"))
	g := security.SystemGrant(importAction, "acme")
	if ok, _ := w.disk.Exists(context.Background(), g, run.FileKey); !ok {
		t.Fatal("the upload is not on the Disk")
	}
	if err := w.service.Discard(context.Background(), ana, "plates", run.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.disk.Exists(context.Background(), g, run.FileKey); ok {
		t.Error("a discarded upload kept its file")
	}
	if _, err := w.service.Confirm(context.Background(), ana, "plates", run.ID); err == nil {
		t.Error("a discarded upload was confirmed")
	}
	if w.pending(t) != 0 {
		t.Error("a job was queued for a discarded upload")
	}
}

func TestOneTenantNeverSeesAnothersRuns(t *testing.T) {
	w := install(t)
	ctx := context.Background()
	mine := w.upload(t, ana, "a.csv", []byte("Placa\nABC1D23\n"))
	w.confirmAndRun(t, ana, mine.ID)
	theirs := w.upload(t, bruno, "b.csv", []byte("Placa\nQWE2R45\n"))
	w.confirmAndRun(t, bruno, theirs.ID)

	if got := w.store.get("acme"); len(got) != 1 || got["ABC1D23"] == "" && len(got) != 1 {
		t.Errorf("acme's table holds %v", got)
	}
	if _, ok := w.store.get("globex")["ABC1D23"]; ok {
		t.Error("a plate of acme was written in globex")
	}

	if _, err := w.service.Find(ctx, bruno, "plates", mine.ID); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("Find of another tenant's run: %v", err)
	}
	if _, _, err := w.service.Open(ctx, bruno, "plates", mine.ID, true); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("Open of another tenant's report: %v", err)
	}
	if _, err := w.service.Confirm(ctx, bruno, "plates", mine.ID); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("Confirm of another tenant's run: %v", err)
	}
	if err := w.service.Discard(ctx, bruno, "plates", mine.ID); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("Discard of another tenant's run: %v", err)
	}
	page, err := w.service.List(ctx, bruno, imports.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Rows[0].ID != theirs.ID {
		t.Errorf("bruno's history holds %d runs", page.Total)
	}
}

func TestOnlyWhoHoldsTheActionImportsOrExports(t *testing.T) {
	w := install(t)
	ctx := context.Background()
	onlyImport := who("acme", "ines", importAction)
	onlyExport := who("acme", "edu", exportAction)

	if _, err := w.service.RequestExport(ctx, onlyImport, "plates", "csv"); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a subject that cannot export exported: %v", err)
	}
	if _, err := w.service.Upload(ctx, onlyExport, "plates", imports.Upload{Name: "a.csv", Body: strings.NewReader("Placa\nABC1D23\n")}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a subject that cannot import imported: %v", err)
	}
	run := w.upload(t, onlyImport, "a.csv", []byte("Placa\nABC1D23\n"))
	// Seeing an import takes the import action: the exporter does not see it.
	if _, err := w.service.Find(ctx, onlyExport, "plates", run.ID); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("the exporter saw an import: %v", err)
	}
	page, err := w.service.List(ctx, onlyExport, imports.ListQuery{})
	if err != nil || page.Total != 0 {
		t.Errorf("the exporter's history: %d runs, %v", page.Total, err)
	}
}

func TestAnExportIsWrittenInTheBackgroundInBothFormatsAndComesBack(t *testing.T) {
	w := install(t)
	ctx := context.Background()
	first := w.upload(t, ana, "a.csv", []byte("Placa;Nota\nABC1D23;com acento é\nQWE2R45;\n"))
	w.confirmAndRun(t, ana, first.ID)

	for _, format := range []string{imports.FormatCSV, imports.FormatXLSX} {
		run, err := w.service.RequestExport(ctx, ana, "plates", format)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != string(imports.StatusQueued) || run.IsImport() {
			t.Fatalf("an export starts %s, kind %s", run.Status, run.Kind)
		}
		w.work(t)
		done, err := w.service.Find(ctx, ana, "plates", run.ID)
		if err != nil || done.Status != string(imports.StatusDone) || done.Total != 2 || !done.HasFile() {
			t.Fatalf("%s export: %+v %v", format, done, err)
		}
		_, file, err := w.service.Open(ctx, ana, "plates", run.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(file.Body)
		_ = file.Body.Close()
		if !strings.HasSuffix(file.Name, "-2026-11-13."+format) {
			t.Errorf("export named %q", file.Name)
		}
		// What was exported imports again: the round trip is the proof the
		// formats are ones the reader takes.
		back, err := w.service.Upload(ctx, ana, "plates", imports.Upload{Name: file.Name, Body: bytes.NewReader(body)})
		if err != nil {
			t.Fatalf("%s: the export was not read back: %v", format, err)
		}
		preview := imports.NewResource(*back).ToArray()["preview"].(imports.Preview)
		if back.Total != 2 || preview.Invalid != 0 || preview.Sample[0].Values["note"] == "" && preview.Sample[1].Values["note"] == "" {
			t.Errorf("%s: read back %d rows, preview %+v", format, back.Total, preview)
		}
	}
}

func TestTheCleanDeletesTheFilesOfAnExpiredRunAndKeepsTheHistory(t *testing.T) {
	w := install(t)
	ctx := context.Background()
	g := security.SystemGrant(importAction, "acme")

	done := w.upload(t, ana, "a.csv", []byte("Placa\nABC1D23\nBAD\n"))
	done = w.confirmAndRun(t, ana, done.ID)
	forgotten := w.upload(t, ana, "b.csv", []byte("Placa\nQWE2R45\n"))
	export, _ := w.service.RequestExport(ctx, ana, "plates", "csv")
	w.work(t)
	export, _ = w.service.Find(ctx, ana, "plates", export.ID)
	other := w.upload(t, bruno, "c.csv", []byte("Placa\nXYZ9K88\n"))

	keys := []string{done.FileKey, done.ReportKey, forgotten.FileKey, export.FileKey, other.FileKey}
	for _, key := range keys {
		if key == "" {
			t.Fatal("a run has no file to clean")
		}
	}

	if err := w.module.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.disk.Exists(ctx, g, done.FileKey); !ok {
		t.Error("the clean deleted the file of a run that has not expired")
	}

	w.clock.advance(8 * 24 * time.Hour)
	// A run made after the days passed is not touched.
	fresh := w.upload(t, ana, "d.csv", []byte("Placa\nNEW1A11\n"))
	if err := w.module.Clean(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys[:4] {
		if ok, _ := w.disk.Exists(ctx, g, key); ok {
			t.Errorf("%s is still on the Disk after the clean", key)
		}
	}
	if ok, _ := w.disk.Exists(ctx, security.SystemGrant(importAction, "globex"), other.FileKey); ok {
		t.Error("the clean left the file of another tenant")
	}
	if ok, _ := w.disk.Exists(ctx, g, fresh.FileKey); !ok {
		t.Error("the clean deleted the file of a run that has not expired")
	}

	kept, err := w.service.Find(ctx, ana, "plates", done.ID)
	if err != nil || kept.Created != 1 || kept.Refused != 1 || kept.HasFile() || kept.HasReport() {
		t.Errorf("the history after the clean: %+v %v", kept, err)
	}
	gone, _ := w.service.Find(ctx, ana, "plates", forgotten.ID)
	if gone.Status != string(imports.StatusDiscarded) {
		t.Errorf("an upload nobody confirmed ends %s, want discarded", gone.Status)
	}
	if _, _, err := w.service.Open(ctx, ana, "plates", done.ID, true); !errors.Is(err, imports.ErrNotFound) {
		t.Errorf("a deleted report was opened: %v", err)
	}
	if _, err := w.service.Confirm(ctx, ana, "plates", forgotten.ID); err == nil {
		t.Error("an upload whose file was cleaned was confirmed")
	}
}

func TestEveryRunIsWrittenToTheActivityLogWithCountsAndNoContent(t *testing.T) {
	w := install(t)
	ctx := context.Background()
	run := w.upload(t, ana, "a.csv", []byte("Placa;Nota\nABC1D23;segredo do cliente\nBAD;x\n"))
	w.confirmAndRun(t, ana, run.ID)
	if _, _, err := w.service.Open(ctx, ana, "plates", run.ID, true); err != nil {
		t.Fatal(err)
	}

	rows, err := w.db.QueryContext(ctx, "SELECT event, properties, description FROM activity_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var event, properties, description string
		if err := rows.Scan(&event, &properties, &description); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
		if strings.Contains(properties, "segredo") || strings.Contains(properties, "ABC1D23") || strings.Contains(description, "ABC1D23") {
			t.Errorf("%s carries the content of a row: %s", event, properties)
		}
		if event == imports.ActivityImportFinished && !strings.Contains(properties, `"refused":1`) {
			t.Errorf("the end of the import has no counts: %s", properties)
		}
	}
	want := []string{imports.ActivityImportStarted, imports.ActivityImportFinished, imports.ActivityFileRead}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}
