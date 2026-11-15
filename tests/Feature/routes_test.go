package feature_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/view"

	imports "github.com/hyz-is/arandu-import"
)

// These tests drive the module the way an application does: register its routes
// on a router and make requests with a session. The views are stand-ins that
// say what data they were handed, because compiling the real ones is the
// published-views test's job.

var signedIn sync.Map

var stubs sync.Once

func stubViews() {
	stubs.Do(func() {
		for _, name := range imports.ViewNames() {
			view.Register(name, func(w io.Writer, data any) error {
				switch d := data.(type) {
				case imports.ShowPageData:
					_, err := fmt.Fprintf(w, "show %s %s polling=%v", d.Run.ID, d.Run.StatusKey, d.Polling)
					return err
				case imports.IndexPageData:
					_, err := fmt.Fprintf(w, "index rows=%d overview=%v", len(d.Rows), d.Overview())
					return err
				}
				_, err := fmt.Fprintf(w, "%T", data)
				return err
			})
		}
	})
}

type site struct {
	*world
	router *fhttp.Router
}

func mount(t *testing.T) site { return mountWith(t, nil) }

func mountWith(t *testing.T, change func(*imports.Config)) site {
	t.Helper()
	stubViews()
	w := installWith(t, change)
	w.sessions.ResolveSubjects(func(_ context.Context, who security.Subject) (security.Subject, error) {
		if s, ok := signedIn.Load(who.Tenant + "/" + who.ID); ok {
			return s.(security.Subject), nil
		}
		return who, nil
	})
	flash := security.NewFlash([]byte(appKey), false)
	router := fhttp.NewRouter().WithFlash(flash).WithRenderer(view.NewRenderer())
	w.module.Routes(router.ForModule(w.module.Name()))
	return site{world: w, router: router}
}

func (s site) signIn(t *testing.T, subject security.Subject) []*http.Cookie {
	t.Helper()
	signedIn.Store(subject.Tenant+"/"+subject.ID, subject)
	rec := httptest.NewRecorder()
	if _, err := s.sessions.Start(context.Background(), rec, security.Subject{ID: subject.ID, Tenant: subject.Tenant}); err != nil {
		t.Fatal(err)
	}
	return rec.Result().Cookies()
}

func (s site) do(t *testing.T, req *http.Request, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s %s panicked: %v", req.Method, req.URL, recovered)
		}
	}()
	s.router.ServeHTTP(rec, req)
	return rec
}

func (s site) get(t *testing.T, target string, cookies []*http.Cookie, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return s.do(t, req, cookies)
}

func (s site) post(t *testing.T, target string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(t, req, cookies)
}

func (s site) postFile(t *testing.T, target, field, name string, body []byte, cookies []*http.Cookie, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		part, err := mw.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(body)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return s.do(t, req, cookies)
}

const base = imports.DefaultPrefix + "/plates"

func TestEveryRouteSendsAVisitorWithNoSessionToSignIn(t *testing.T) {
	s := mount(t)
	for _, c := range []struct{ method, target string }{
		{"GET", imports.DefaultPrefix}, {"GET", base}, {"POST", base}, {"GET", base + "/template/csv"}, {"POST", base + "/export"},
		{"GET", base + "/runs/r1"}, {"GET", base + "/runs/r1/progress"}, {"POST", base + "/runs/r1/confirm"},
		{"POST", base + "/runs/r1/discard"}, {"GET", base + "/runs/r1/refused"}, {"GET", base + "/runs/r1/file"},
	} {
		rec := s.do(t, httptest.NewRequest(c.method, c.target, nil), nil)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/auth/login") {
			t.Errorf("%s %s with no session: %d to %q, want 303 to the sign-in", c.method, c.target, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestASignedInSubjectWithoutTheActionIsRefused(t *testing.T) {
	s := mount(t)
	cookies := s.signIn(t, who("acme", "nobody"))
	for _, target := range []string{imports.DefaultPrefix, base, base + "/template/csv", base + "/runs/r1"} {
		if rec := s.get(t, target, cookies); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: %d, want 403", target, rec.Code)
		}
	}
	if rec := s.postFile(t, base, "file", "a.csv", []byte("Placa\nABC1D23\n"), cookies); rec.Code != http.StatusForbidden {
		t.Errorf("an upload without the action: %d, want 403", rec.Code)
	}
	if rec := s.post(t, base+"/export", url.Values{"format": {"csv"}}, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("an export without the action: %d, want 403", rec.Code)
	}
	if s.pending(t) != 0 || s.store.writes != 0 {
		t.Error("a refused request queued a job or wrote a row")
	}
}

func TestTheWholeFlowThroughTheRoutes(t *testing.T) {
	s := mount(t)
	cookies := s.signIn(t, ana)

	// The two pages.
	if rec := s.get(t, imports.DefaultPrefix, cookies); rec.Code != 200 || !strings.Contains(rec.Body.String(), "overview=true") {
		t.Errorf("overview: %d %q", rec.Code, rec.Body.String())
	}
	if rec := s.get(t, base, cookies); rec.Code != 200 || !strings.Contains(rec.Body.String(), "overview=false") {
		t.Errorf("definition page: %d %q", rec.Code, rec.Body.String())
	}
	if rec := s.get(t, imports.DefaultPrefix+"/nope", cookies); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown definition: %d, want 404", rec.Code)
	}

	// The template downloads in both formats, never inline.
	for format, kind := range map[string]string{"csv": "text/csv", "xlsx": "spreadsheetml"} {
		rec := s.get(t, base+"/template/"+format, cookies)
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), kind) ||
			!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("template %s: %d %v", format, rec.Code, rec.Header())
		}
	}
	if rec := s.get(t, base+"/template/pdf", cookies); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown template format: %d, want 404", rec.Code)
	}

	// The upload redirects to the run's page, whose state is the preview.
	rec := s.postFile(t, base, "file", "placas.csv", []byte("Placa;Nota\nABC1D23;ok\nBAD;x\n"), cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %q", rec.Code, rec.Body.String())
	}
	runURL := rec.Header().Get("Location")
	if !strings.HasPrefix(runURL, base+"/runs/") {
		t.Fatalf("the upload went to %q", runURL)
	}
	if rec := s.get(t, runURL, cookies); rec.Code != 200 || !strings.Contains(rec.Body.String(), "uploaded polling=false") {
		t.Errorf("the preview page: %d %q", rec.Code, rec.Body.String())
	}

	// A browser that has scripts sends the same upload as a YPY request and
	// is told where to go.
	rec = s.postFile(t, base, "file", "placas.csv", []byte("Placa\nXYZ9K88\n"), cookies, "YPY-Request", "true")
	if rec.Header().Get("YPY-Redirect") == "" {
		t.Errorf("a YPY upload was not redirected: %d %v", rec.Code, rec.Header())
	}

	// Confirm queues, the page polls, the fragment is the state alone.
	if rec := s.post(t, runURL+"/confirm", nil, cookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm: %d", rec.Code)
	}
	if rec := s.get(t, runURL+"/progress", cookies, "YPY-Request", "true"); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), "queued polling=true") || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("progress while queued: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	s.work(t)
	if rec := s.get(t, runURL+"/progress", cookies, "YPY-Request", "true"); !strings.Contains(rec.Body.String(), "done polling=false") {
		t.Errorf("progress once done must stop polling: %q", rec.Body.String())
	}

	// The report of the refused rows downloads, and the run is JSON on request.
	rec = s.get(t, runURL+"/refused", cookies)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Placa inválida") ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") ||
		rec.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Errorf("report: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	rec = s.get(t, runURL, cookies, "Accept", "application/json")
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Data["status"] != "done" || body.Data["refused"].(float64) != 1 || body.Data["percent"].(float64) != 100 {
		t.Errorf("json: %v %s", err, rec.Body.String())
	}
	for _, leaked := range []string{"tenant", "file_key", "report_key", "acme"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Errorf("the run's JSON carries %q", leaked)
		}
	}

	// An export is asked for, done, and downloaded.
	rec = s.post(t, base+"/export", url.Values{"format": {"xlsx"}}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("export: %d", rec.Code)
	}
	exportURL := rec.Header().Get("Location")
	s.work(t)
	rec = s.get(t, exportURL+"/file", cookies)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "spreadsheetml") || rec.Body.Len() == 0 {
		t.Errorf("export file: %d %v", rec.Code, rec.Header())
	}
	if rec := s.post(t, base+"/export", url.Values{"format": {"exe"}}, cookies); rec.Code == http.StatusSeeOther && strings.Contains(rec.Header().Get("Location"), "/runs/") {
		t.Error("an export in a format nobody offers was queued")
	}

	// Another tenant, same addresses: not found.
	other := s.signIn(t, bruno)
	for _, target := range []string{runURL, runURL + "/progress", runURL + "/refused", exportURL + "/file"} {
		if rec := s.get(t, target, other); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as another tenant: %d, want 404", target, rec.Code)
		}
	}
	if rec := s.post(t, runURL+"/confirm", nil, other); rec.Code != http.StatusNotFound {
		t.Errorf("confirm as another tenant: %d, want 404", rec.Code)
	}
}

func TestAnUploadWithNoFileOrTooBigGoesBackWithAMessage(t *testing.T) {
	s := mount(t)
	cookies := s.signIn(t, ana)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no field":  s.postFile(t, base, "", "", nil, cookies),
		"no file":   s.postFile(t, base, "file", "", nil, cookies),
		"too small": s.postFile(t, base, "file", "a.csv", []byte("  "), cookies),
		"not csv":   s.postFile(t, base, "file", "a.pdf", []byte("%PDF-1.4"), cookies),
	} {
		if rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "/runs/") {
			t.Errorf("%s: %d to %q, want 303 back where it came from", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	big := bytes.Repeat([]byte("ABC1D23\n"), 2<<20)
	if rec := s.postFile(t, base, "file", "big.csv", big, cookies); rec.Code != http.StatusSeeOther || strings.Contains(rec.Header().Get("Location"), "/runs/") {
		t.Errorf("a file over the limit: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if s.pending(t) != 0 {
		t.Error("a rejected upload queued a job")
	}
}

func TestTheModuleRegistersItsRoutesUnderItsPrefixAndNeverUnderTheFrameworks(t *testing.T) {
	w := install(t)
	for _, prefix := range []string{"/_arandu", "/_arandu/imports"} {
		cfg := w.module
		_ = cfg
		_, err := imports.New(imports.Config{Definitions: []imports.Definition{w.store.definition()}, Queue: w.jobs, Files: w.disk, Prefix: prefix}, w.db, w.sessions)
		if err == nil {
			t.Errorf("the prefix %s was accepted", prefix)
		}
	}
	urls := w.module.URLs()
	for name, got := range map[string]string{
		urls.Index(): "/imports", urls.Definition("a b"): "/imports/a%20b", urls.Template("plates", "csv"): "/imports/plates/template/csv",
		urls.Run("plates", "r1"): "/imports/plates/runs/r1", urls.Progress("plates", "r1"): "/imports/plates/runs/r1/progress",
		urls.Refused("plates", "r1"): "/imports/plates/runs/r1/refused", urls.File("plates", "r1"): "/imports/plates/runs/r1/file",
		urls.Export("plates"): "/imports/plates/export",
	} {
		if name != got {
			t.Errorf("url %q, want %q", name, got)
		}
	}
	pt := imports.NewURLs("/painel/importacoes", imports.PortuguesePaths)
	if pt.Run("plates", "r1") != "/painel/importacoes/plates/execucoes/r1" || pt.Confirm("plates", "r1") != "/painel/importacoes/plates/execucoes/r1/confirmar" {
		t.Errorf("portuguese urls: %s %s", pt.Run("plates", "r1"), pt.Confirm("plates", "r1"))
	}
}

func TestTheActingSubjectIsTheOneTheApplicationScopesTheSessionTo(t *testing.T) {
	// The person signs in as themselves, with no action. The application says
	// which space they work in and what they may do there: the module acts as
	// that subject, in that tenant, and nothing a request says changes it.
	selected := "globex"
	s := mountWith(t, func(cfg *imports.Config) {
		cfg.Scope = func(_ *fhttp.Context, who security.Subject) (security.Subject, error) {
			who.Tenant, who.Actions = selected, []security.Action{importAction}
			return who, nil
		}
	})
	cookies := s.signIn(t, who("acme", "viviane"))

	rec := s.postFile(t, base, "file", "a.csv", []byte("Placa\nABC1D23\n"), cookies)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/runs/") {
		t.Fatalf("an upload as the scoped subject: %d %q", rec.Code, rec.Body.String())
	}
	runURL := rec.Header().Get("Location")
	if got := s.get(t, runURL, cookies); got.Code != 200 {
		t.Errorf("the run of the space the person selected: %d", got.Code)
	}
	if n := runsOf(t, s.world, "globex"); n != 1 {
		t.Errorf("the run is in globex %d times, want once", n)
	}
	if n := runsOf(t, s.world, "acme"); n != 0 {
		t.Errorf("the session's own tenant has %d runs: the request chose where it is written", n)
	}

	// Another selection in the same session: the run is not found there.
	selected = "initech"
	if got := s.get(t, runURL, cookies); got.Code != http.StatusNotFound {
		t.Errorf("the run seen from another selected space: %d, want 404", got.Code)
	}
}

func TestAScopeThatFailsStopsTheRequest(t *testing.T) {
	s := mountWith(t, func(cfg *imports.Config) {
		cfg.Scope = func(*fhttp.Context, security.Subject) (security.Subject, error) {
			return security.Subject{}, security.ErrForbidden
		}
	})
	cookies := s.signIn(t, ana)
	if rec := s.get(t, base, cookies); rec.Code != http.StatusForbidden {
		t.Errorf("a failed scope: %d, want 403", rec.Code)
	}
}

// runsOf counts the runs a tenant holds, through the service.
func runsOf(t *testing.T, w *world, tenant string) int {
	t.Helper()
	page, err := w.service.List(context.Background(), who(tenant, "counter", importAction), imports.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}
