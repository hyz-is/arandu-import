package unit_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
)

// viewCompiler is the command line whose view compiler the published views are
// held against, at the release an application builds them with.
//
// It is a version and never "latest", so this test changes its answer when this
// line changes and never because somebody published something. Moving it is
// how a stricter compiler is taken on: raise it, run the suite, and fix the
// view it refuses here rather than in every application that publishes it.
const viewCompiler = "github.com/arandu-io/aru@v0.81.3"

// TestEveryPublishedViewCompiles publishes the views into a project of their
// own, builds them with the view compiler an application runs, and compiles
// what it wrote.
//
// Nothing else in this repository reads the markup. The sources open with a
// build tag, so go build, go vet and go test never look past it, and the view
// compiler refuses what the Go compiler would accept -- a value written into an
// address behind text it cannot read, a value in a position no escape covers.
// A view it refuses is a view that stops the build of every application that
// publishes it, and without this test the first place to say so is somebody
// else's terminal.
//
// The project is laid out as an application's is once it has published: each
// file where the publication writes it, and a go.mod that resolves this package
// to the checkout under test. What the view compiler writes then goes through
// the Go compiler, which is what catches a field the markup names and the page
// data does not have.
//
// It does not skip. A gate that steps aside when its input is missing is how
// the views stopped compiling with nothing here saying so.
func TestEveryPublishedViewCompiles(t *testing.T) {
	root := packageRoot(t)
	project := t.TempDir()

	publications, err := foundation.Publications(module(t))
	if err != nil {
		t.Fatalf("reading what the module publishes: %v", err)
	}
	var published int
	for _, publication := range publications {
		from := publication.From
		if from == "" {
			from = "."
		}
		err := fs.WalkDir(publication.Files, from, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative := name
			if from != "." {
				relative = strings.TrimPrefix(strings.TrimPrefix(name, from), "/")
			}
			body, err := fs.ReadFile(publication.Files, name)
			if err != nil {
				return err
			}
			target := filepath.Join(project, filepath.FromSlash(path.Join(publication.To, relative)))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			published++
			return os.WriteFile(target, body, 0o644)
		})
		if err != nil {
			t.Fatalf("publishing the %s files: %v", publication.Tag, err)
		}
	}
	if published == 0 {
		t.Fatal("the module publishes no file, so this gate would compile nothing")
	}

	// The application's go.mod names this package and points it at the
	// checkout, so the views compile against the code beside them rather than
	// against a release. The sums come from here: the application needs no
	// module this package does not already require.
	goMod := readReleaseFile(t, root, "go.mod")
	language := captureReleaseValue(t, goMod, `(?m)^go ([0-9.]+)$`, "go directive in go.mod")
	modulePath := captureReleaseValue(t, goMod, `(?m)^module (\S+)$`, "module path in go.mod")
	manifest := "module example.com/application\n\n" +
		"go " + language + "\n\n" +
		"require " + modulePath + " v0.0.0-00010101000000-000000000000\n\n" +
		"replace " + modulePath + " => " + strconv.Quote(root) + "\n"
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.sum"), []byte(readReleaseFile(t, root, "go.sum")), 0o644); err != nil {
		t.Fatal(err)
	}

	inProject(t, project, "the published views do not compile with "+viewCompiler+", so no application that publishes them can build",
		"go", "run", viewCompiler, "view:build")
	inProject(t, project, "the views "+viewCompiler+" compiled do not build against this package",
		"go", "build", "./...")

	// Compiling is not drawing. Each screen is rendered once, with a row of
	// every kind and every action switched on, under a layout that draws the
	// content section alone -- so a page that compiles and then fails on its
	// own data, or draws a key instead of a sentence, fails here.
	probe := filepath.Join(project, "renderprobe")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "main.go"), []byte(renderProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	inProject(t, project, "a published view does not render the page data its handler hands it",
		"go", "run", "./renderprobe")
}

// renderProbe renders every screen with sample data and checks what it drew.
const renderProbe = `package main

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"

	"github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
	imports "github.com/hyz-is/arandu-import"

	_ "example.com/application/storage/framework/views/modules/imports"
)

func main() {
	view.RegisterLayout("layouts.app", func(w io.Writer, _ any, sections map[string]func(io.Writer) error) error {
		return sections["content"](w)
	})
	sc := imports.Screen{
		Labels: imports.Labels{Locale: "pt-BR"},
		URLs:   imports.NewURLs("/painel/importacoes", imports.PortuguesePaths),
	}
	def := &imports.DefinitionView{Key: "lista", Label: "Lista de permitidos", Description: "As placas que você permite.",
		URL: "/painel/importacoes/lista", CanImport: true, CanExport: true}
	history := []imports.RunRow{{ID: "r1", Definition: "lista", DefinitionLabel: "Lista de permitidos", Kind: "import", KindLabel: "Importação",
		Status: "Concluída", StatusKey: "done", StatusVariant: "outline", FileName: "placas.xlsx", Format: "XLSX",
		Result: "Novas: 3 · Atualizadas: 1 · Sem mudança: 0 · Recusadas: 2", When: "13/11/2026 12:00", URL: "/painel/importacoes/lista/r1"}}
	columns := []imports.ColumnView{{Label: "Placa", Required: true, Hint: "ABC1D23"}, {Label: "Nota"}}
	line := func(n int, plate, note, reason string) imports.LineRow {
		return imports.LineRow{Number: n, Cells: []string{plate, note}, Reason: reason}
	}
	run := func(status, key string, extra func(*imports.RunRow)) imports.RunRow {
		r := imports.RunRow{ID: "r1", Definition: "lista", DefinitionLabel: "Lista de permitidos", Kind: "import", KindLabel: "Importação",
			Status: status, StatusKey: key, FileName: "placas.xlsx", Format: "XLSX", Size: "12 KB", Total: 10, When: "13/11/2026 12:00",
			URL: "/painel/importacoes/lista/r1", IsImport: true}
		if extra != nil {
			extra(&r)
		}
		return r
	}
	state := func(r imports.RunRow, change func(*imports.ShowPageData)) imports.ShowPageData {
		d := imports.ShowPageData{Screen: sc, Run: r, Definition: *def, BackURL: "/painel/importacoes/lista",
			ProgressURL: "/painel/importacoes/lista/r1/progresso", ConfirmURL: "/painel/importacoes/lista/r1/confirmar",
			DiscardURL: "/painel/importacoes/lista/r1/descartar", RefusedURL: "/painel/importacoes/lista/r1/recusadas",
			FileURL: "/painel/importacoes/lista/r1/arquivo", Heads: []string{"Placa", "Nota"}, Retention: 7}
		if change != nil {
			change(&d)
		}
		return d
	}

	pages := []struct {
		name string
		data any
		want []string
		not  []string
	}{
		{imports.ViewIndex, imports.IndexPageData{Screen: sc, Definition: def, UploadURL: "/painel/importacoes/lista", Accept: ".csv,.xlsx",
			MaxMB: "10", MaxRows: 5000, Columns: columns, TemplateCSV: "/painel/importacoes/lista/modelo/csv", TemplateXLSX: "/painel/importacoes/lista/modelo/xlsx",
			ExportURL: "/painel/importacoes/lista/exportar", CanImport: true, CanExport: true, Retention: 7, Rows: history, URL: "/painel/importacoes/lista",
			CurrentPage: 1, Pages: 1, Total: 1, PerPage: 25, Pager: components.Paginator(nil)},
			[]string{"Lista de permitidos", "As placas que você permite.", "enctype=\"multipart/form-data\"", "type=\"file\"", ".csv,.xlsx", "Arraste o arquivo para cá",
				"CSV ou Excel (.xlsx), até 10 MB e 5000 linhas.", "Revisar arquivo", "Baixar modelo Excel", "/painel/importacoes/lista/modelo/xlsx",
				"/painel/importacoes/lista/modelo/csv", "Placa", "obrigatória", "Exportar Excel", "Exportar CSV", "/painel/importacoes/lista/exportar",
				"placas.xlsx", "Concluída", "/painel/importacoes/lista/r1", "Novas: 3", "_token", "Histórico", "Buscar em Histórico"}, nil},
		{imports.ViewIndex, imports.IndexPageData{Screen: sc, Definition: def, UploadURL: "/painel/importacoes/lista", Columns: columns, CanImport: false,
			CanExport: false, CurrentPage: 1, Pages: 1, PerPage: 25},
			[]string{"Nenhuma importação ainda"}, []string{"type=\"file\"", "Exportar Excel"}},
		{imports.ViewIndex, imports.IndexPageData{Screen: sc, Definitions: []imports.DefinitionView{*def}, Rows: history, URL: "/painel/importacoes",
			CurrentPage: 1, Pages: 1, Total: 1, PerPage: 25, Pager: components.Paginator(nil)},
			[]string{"Importar e exportar", "Lista de permitidos", "Abrir", "/painel/importacoes/lista", "placas.xlsx"}, []string{"type=\"file\""}},
		{imports.ViewIndex, imports.IndexPageData{Screen: sc, CurrentPage: 1, Pages: 1, PerPage: 25, URL: "/painel/importacoes"},
			[]string{"Nada para importar ou exportar"}, nil},
		{imports.ViewShow, state(run("Aguardando confirmação", "uploaded", nil), func(d *imports.ShowPageData) {
			d.Header, d.Valid, d.Invalid, d.CanConfirm, d.MoreProblem = true, 7, 3, true, 1
			d.Mapped = []imports.MappedRow{{Label: "Placa", Heading: "Placas", Required: true, Found: true}, {Label: "Nota", Found: false}}
			d.Ignored = "Cor"
			d.Sample = []imports.LineRow{line(2, "ABC1D23", "Carro", ""), line(3, "ABC", "", "Placa inválida")}
			d.Problems = []imports.LineRow{line(3, "ABC", "", "Placa inválida")}
		}),
			[]string{"Confira antes de importar", "placas.xlsx", "Prontas para importar", "Serão recusadas", "Placa", "← Placas", "não encontrada",
				"A primeira linha foi lida como cabeçalho.", "Colunas ignoradas: Cor.", "ABC1D23", "Placa inválida", "Linhas com problema", "e mais 1 linhas",
				"Importar agora", "/painel/importacoes/lista/r1/confirmar", "Descartar", "/painel/importacoes/lista/r1/descartar", "_token",
				"Enviada em 13/11/2026 12:00", "Importação"}, []string{"hx-trigger", "Baixar recusadas"}},
		{imports.ViewShow, state(run("Aguardando confirmação", "uploaded", nil), func(d *imports.ShowPageData) { d.Valid, d.Invalid = 0, 10; d.CanConfirm = true }),
			[]string{"Nenhuma linha está pronta para importar"}, []string{"Importar agora"}},
		{imports.ViewShow, state(run("Em andamento", "running", func(r *imports.RunRow) { r.Active, r.Processed, r.Percent = true, 4, 40 }),
			func(d *imports.ShowPageData) { d.Polling = true }),
			[]string{"Importando", "4 de 10 linhas lidas.", "hx-get=\"/painel/importacoes/lista/r1/progresso\"", "every 1s", "role=\"progressbar\""}, []string{"Importar agora"}},
		{imports.ViewState, state(run("Concluída", "done", func(r *imports.RunRow) {
			r.IsFinished, r.Created, r.Updated, r.Unchanged, r.Refused, r.Processed, r.Percent, r.Finished = true, 5, 2, 1, 2, 10, 100, "13/11/2026 12:01"
		}), func(d *imports.ShowPageData) {
			d.HasReport, d.MoreProblem = true, 0
			d.Problems = []imports.LineRow{line(4, "XYZ", "x", "Placa inválida"), line(9, "", "y", "Placa inválida")}
		}),
			[]string{"Importação concluída", "Novas", "Atualizadas", "Sem mudança", "Recusadas", "Linhas recusadas", "Placa inválida", "Baixar recusadas (CSV)",
				"/painel/importacoes/lista/r1/recusadas", "Importar outro arquivo"}, []string{"hx-trigger", "Importar agora"}},
		{imports.ViewState, state(run("Falhou", "failed", nil), func(d *imports.ShowPageData) { d.Failure = "Não conseguimos ler o arquivo enviado. Envie de novo." }),
			[]string{"Não deu para concluir", "Não conseguimos ler o arquivo enviado."}, []string{"hx-trigger"}},
		{imports.ViewState, state(run("Descartada", "discarded", nil), nil), []string{"Importação descartada", "nada foi gravado"}, nil},
		{imports.ViewState, state(run("Em andamento", "running", func(r *imports.RunRow) { r.Active, r.IsImport, r.IsExport, r.Kind = true, false, true, "export" }),
			func(d *imports.ShowPageData) { d.Polling, d.Indeterminate = true, true }),
			[]string{"Gerando o arquivo", "hx-get"}, nil},
		{imports.ViewState, state(run("Concluída", "done", func(r *imports.RunRow) {
			r.IsImport, r.IsExport, r.Kind, r.IsFinished, r.Total, r.Format, r.Size = false, true, "export", true, 42, "XLSX", "3 KB"
		}), func(d *imports.ShowPageData) { d.HasFile = true }),
			[]string{"Arquivo pronto", "42 linhas no arquivo", "Baixar arquivo (XLSX, 3 KB)", "/painel/importacoes/lista/r1/arquivo"}, []string{"Importar outro arquivo"}},
	}

	inline := regexp.MustCompile(` + "`" + `(?i)(<script|\sstyle=|\son[a-z]+=|javascript:)` + "`" + `)
	failed := false
	for _, p := range pages {
		rec := httptest.NewRecorder()
		if err := view.NewRenderer().Render(context.Background(), rec, 200, p.name, p.data); err != nil {
			fmt.Printf("%s: %v\n", p.name, err)
			failed = true
			continue
		}
		body := rec.Body.String()
		for _, want := range p.want {
			if !strings.Contains(body, want) {
				fmt.Printf("%s: the page does not say %q\n", p.name, want)
				failed = true
			}
		}
		for _, not := range p.not {
			if strings.Contains(body, not) {
				fmt.Printf("%s: the page says %q, which this data must not draw\n", p.name, not)
				failed = true
			}
		}
		if strings.Contains(body, "imports.screen.") || strings.Contains(body, "imports.field.") || strings.Contains(body, "imports.control.") {
			fmt.Printf("%s: the page draws a catalogue key instead of its sentence\n", p.name)
			failed = true
		}
		// The content security policy is 'self': no inline script, no inline
		// style, no event attribute and no javascript: address.
		if found := inline.FindString(body); found != "" {
			fmt.Printf("%s: the page carries %q, which a policy of 'self' refuses\n", p.name, found)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
`

// inProject runs one command in the scratch project, outside any workspace,
// and fails with what it printed.
//
// -mod=mod lets the go command fill in the requirements the application's
// go.mod leaves to this package's, which is what an application's own `go mod
// tidy` would have written.
func inProject(t *testing.T, dir, failure string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -mod=mod"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s:\n$ %s %s\n%s", failure, name, strings.Join(args, " "), output)
	}
}
