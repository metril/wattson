package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/metril/wattson/internal/auth"
)

type pingIn struct {
	Body struct {
		Msg string `json:"msg" doc:"Message to echo."`
	}
}

type pingOut struct {
	Body struct {
		Msg string `json:"msg" doc:"Echoed message."`
	}
}

func registerPing(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/test/ping", ID: "testPing", Tag: "test",
		Summary: "Test op", Description: "Test-only control operation.", Scope: auth.ScopeControl},
		func(_ context.Context, in *pingIn) (*pingOut, error) {
			out := &pingOut{}
			out.Body.Msg = in.Body.Msg
			return out, nil
		})
}

func errCode(t *testing.T, b []byte) string {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("body %q is not an error envelope: %v", b, err)
	}
	return e.Error.Code
}

func TestScopeEnforced(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing))
	body := map[string]string{"msg": "hi"}

	resp, b := ts.do(t, nil, "POST", "/api/test/ping", body)
	if resp.StatusCode != 401 || errCode(t, b) != "unauthorized" {
		t.Errorf("anonymous: %d %s, want 401 unauthorized", resp.StatusCode, b)
	}
	read := ts.mintToken(t, "reader", false, "read")
	resp, b = ts.do(t, nil, "POST", "/api/test/ping", body, "Authorization", "Bearer "+read)
	if resp.StatusCode != 403 || errCode(t, b) != "forbidden" {
		t.Errorf("read token: %d %s, want 403 forbidden", resp.StatusCode, b)
	}
	ctl := ts.mintToken(t, "driver", false, "control")
	resp, b = ts.do(t, nil, "POST", "/api/test/ping", body, "Authorization", "Bearer "+ctl)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"hi"`) {
		t.Errorf("control token: %d %s, want 200", resp.StatusCode, b)
	}
}

func TestJSONContentTypeRequired(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing))
	ctl := ts.mintToken(t, "driver", false, "control")
	req, _ := http.NewRequest("POST", ts.URL+"/api/test/ping", strings.NewReader(`{"msg":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+ctl)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&e)
	if resp.StatusCode != 415 || e.Error.Code != "unsupported_media_type" {
		t.Errorf("got %d %+v, want 415 unsupported_media_type", resp.StatusCode, e)
	}
}

func TestValidationErrorEnvelope(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing))
	ctl := ts.mintToken(t, "driver", false, "control")
	resp, b := ts.do(t, nil, "POST", "/api/test/ping", map[string]any{"msg": 5}, "Authorization", "Bearer "+ctl)
	if resp.StatusCode != 400 || errCode(t, b) != "invalid_request" || !strings.Contains(string(b), "body.msg") {
		t.Errorf("got %d %s, want 400 invalid_request mentioning body.msg", resp.StatusCode, b)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	resp, b := ts.do(t, nil, "GET", "/api/nope", nil)
	if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
		t.Errorf("got %d %s, want 404 not_found", resp.StatusCode, b)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	resp, b := ts.do(t, nil, "GET", "/healthz", nil)
	if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"status":"ok","version":"test"}` {
		t.Errorf("got %d %s", resp.StatusCode, b)
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	resp, _ := ts.do(t, nil, "GET", "/healthz", nil)
	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "default-src 'self'; connect-src 'self' https://tiles.openfreemap.org; img-src 'self' data: blob: https://tiles.openfreemap.org; worker-src 'self' blob:; child-src blob:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestDocsServed(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	resp, _ := ts.do(t, nil, "GET", "/docs", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("/docs: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, b := ts.do(t, nil, "GET", "/openapi.json", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"/healthz"`) {
		t.Errorf("/openapi.json: %d, missing /healthz", resp.StatusCode)
	}
}

// uiBuilt reports whether the embedded web build exists, so SPA tests pick
// their expectation explicitly.
func uiBuilt() bool {
	_, err := os.Stat("../web/dist/index.html")
	return err == nil
}

func TestSPAFallbackMethods(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	want := 503
	if uiBuilt() {
		want = 200
	}
	if resp, _ := ts.do(t, nil, "GET", "/riders", nil); resp.StatusCode != want {
		t.Errorf("GET /riders: %d, want %d", resp.StatusCode, want)
	}
	resp, b := ts.do(t, nil, "POST", "/riders", nil)
	if resp.StatusCode != 405 || errCode(t, b) != "method_not_allowed" || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST /riders: %d %s allow=%q, want 405", resp.StatusCode, b, resp.Header.Get("Allow"))
	}
}

func TestAPIPrefixIsJSON404(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	for _, path := range []string{"/api", "/api/", "/api/nope/deeper"} {
		resp, b := ts.do(t, nil, "GET", path, nil)
		if resp.StatusCode != 404 || errCode(t, b) != "not_found" {
			t.Errorf("GET %s: %d %s, want 404 not_found", path, resp.StatusCode, b)
		}
	}
}

func TestContentSecurityPolicy(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	const strict = "default-src 'self'; connect-src 'self' https://tiles.openfreemap.org; img-src 'self' data: blob: https://tiles.openfreemap.org; worker-src 'self' blob:; child-src blob:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
	for _, path := range []string{"/healthz", "/riders", "/api/nope", "/openapi.json"} {
		resp, _ := ts.do(t, nil, "GET", path, nil)
		if got := resp.Header.Get("Content-Security-Policy"); got != strict {
			t.Errorf("%s: CSP = %q, want the strict policy", path, got)
		}
	}
	resp, _ := ts.do(t, nil, "GET", "/docs", nil)
	got := resp.Header.Get("Content-Security-Policy")
	if got == strict || !strings.Contains(got, "script-src https://unpkg.com/") || !strings.Contains(got, "default-src 'none'") {
		t.Errorf("/docs: CSP = %q, want huma's own policy allowing only the pinned unpkg files", got)
	}
}

type noBodyIn struct{}

type noBodyOut struct {
	Body struct {
		Boom string `json:"boom" doc:"Unused."`
	}
}

func registerBoom(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/test/nobody", ID: "testNoBody", Tag: "test",
		Summary: "No body", Description: "Test-only op with no request body.", Scope: auth.ScopeRead},
		func(context.Context, *noBodyIn) (*noBodyOut, error) {
			return nil, errors.New("db password=hunter2 leaked")
		})
	register(api, route{Method: http.MethodGet, Path: "/api/test/conflict", ID: "testConflict", Tag: "test",
		Summary: "Conflict", Description: "Test-only op returning a 409.", Scope: auth.ScopeRead, Errors: []int{409}},
		func(context.Context, *noBodyIn) (*noBodyOut, error) {
			return nil, apiErr(409, "conflict", "already there")
		})
	register(api, route{Method: http.MethodGet, Path: "/api/test/apierr500", ID: "testAPIErr500", Tag: "test",
		Summary: "500", Description: "Test-only op returning a 500 apiErr.", Scope: auth.ScopeRead},
		func(context.Context, *noBodyIn) (*noBodyOut, error) {
			return nil, apiErr(503, "unavailable", "secret detail")
		})
}

func TestHandlerErrorsAreLoggedAndSanitised(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerBoom))
	tok := "Bearer " + ts.mintToken(t, "reader", false, "read")

	resp, b := ts.do(t, nil, "POST", "/api/test/nobody", nil, "Authorization", tok)
	if resp.StatusCode != 500 || errCode(t, b) != "internal_error" || strings.Contains(string(b), "hunter2") {
		t.Errorf("plain error: %d %s, want sanitised 500 internal_error", resp.StatusCode, b)
	}
	logs := ts.Log.String()
	if !strings.Contains(logs, "op=testNoBody") || !strings.Contains(logs, "hunter2") || !strings.Contains(logs, "level=ERROR") {
		t.Errorf("log missing op ID / error: %s", logs)
	}

	resp, b = ts.do(t, nil, "GET", "/api/test/apierr500", nil, "Authorization", tok)
	if resp.StatusCode != 500 || strings.Contains(string(b), "secret detail") {
		t.Errorf("apiErr 5xx: %d %s, want sanitised 500", resp.StatusCode, b)
	}
	if !strings.Contains(ts.Log.String(), "op=testAPIErr500") {
		t.Errorf("5xx apiErr not logged: %s", ts.Log.String())
	}

	before := ts.Log.String()
	resp, b = ts.do(t, nil, "GET", "/api/test/conflict", nil, "Authorization", tok)
	if resp.StatusCode != 409 || errCode(t, b) != "conflict" {
		t.Errorf("4xx apiErr: %d %s, want 409 conflict", resp.StatusCode, b)
	}
	if ts.Log.String() != before {
		t.Errorf("4xx apiErr was logged: %s", strings.TrimPrefix(ts.Log.String(), before))
	}
}

func TestClientGoneErrorLoggedAtDebug(t *testing.T) {
	t.Parallel()
	run := func(ctx context.Context) string {
		var logs syncBuf
		r := chi.NewRouter()
		r.Use(withLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		register(newHumaAPI(r), route{Method: http.MethodGet, Path: "/api/t", ID: "testGone", Tag: "test",
			Summary: "Gone", Description: "Test-only op that fails."},
			func(context.Context, *noBodyIn) (*noBodyOut, error) {
				return nil, errors.New("context canceled mid-query")
			})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/api/t", nil).WithContext(ctx))
		if rec.Code != 500 {
			t.Fatalf("status %d, want 500", rec.Code)
		}
		return logs.String()
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if got := run(gone); strings.Contains(got, "level=ERROR") || !strings.Contains(got, "level=DEBUG") {
		t.Errorf("client gone: log %q, want Debug and no ERROR", got)
	}
	if got := run(context.Background()); !strings.Contains(got, "level=ERROR") {
		t.Errorf("live client: log %q, want ERROR", got)
	}
}

func TestBodyWithoutContentTypeRefused(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing))
	tok := "Bearer " + ts.mintToken(t, "driver", false, "control")
	do := func(body io.Reader, chunked bool) *http.Response {
		req, _ := http.NewRequest("POST", ts.URL+"/api/test/ping", body)
		if chunked {
			req.ContentLength = -1
		}
		req.Header.Set("Authorization", tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	if r := do(strings.NewReader(`{"msg":"x"}`), false); r.StatusCode != 415 {
		t.Errorf("body, no Content-Type: %d, want 415", r.StatusCode)
	}
	if r := do(io.NopCloser(strings.NewReader(`{"msg":"x"}`)), true); r.StatusCode != 415 {
		t.Errorf("chunked body, no Content-Type: %d, want 415", r.StatusCode)
	}
	if r := do(nil, false); r.StatusCode == 415 {
		t.Errorf("empty request: %d, want a non-415 (validation) status", r.StatusCode)
	}
}

func TestRecovererKeepsStartedResponse(t *testing.T) {
	t.Parallel()
	var logs syncBuf
	d := Deps{Logger: slog.New(slog.NewTextHandler(&logs, nil))}

	before := d.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	before.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal_error") {
		t.Errorf("panic before write: %d %s", rec.Code, rec.Body.String())
	}

	after := d.recoverer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial"))
		panic("late")
	}))
	rec = httptest.NewRecorder()
	after.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusAccepted || rec.Body.String() != "partial" {
		t.Errorf("panic after write: %d %q, want 202 \"partial\" untouched", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "late") {
		t.Errorf("late panic not logged: %s", logs.String())
	}
}

func TestTypedOperationSpec(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing, registerBoom))
	_, b := ts.do(t, nil, "GET", "/openapi.json", nil)
	var spec struct {
		Paths map[string]map[string]struct {
			Description string
			Security    []map[string][]string
			Responses   map[string]json.RawMessage
		}
		Components struct{ Schemas map[string]schemaDoc }
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	op := spec.Paths["/api/test/ping"]["post"]
	if len(op.Security) != 2 || !strings.Contains(op.Description, "`control` scope") {
		t.Errorf("security=%v description=%q", op.Security, op.Description)
	}
	for _, c := range []string{"200", "400", "401", "403", "415"} {
		if _, ok := op.Responses[c]; !ok {
			t.Errorf("response %s not documented", c)
		}
	}
	for _, c := range []string{"422", "default"} {
		if _, ok := op.Responses[c]; ok {
			t.Errorf("response %s should be stripped", c)
		}
	}
	if _, ok := spec.Paths["/api/test/nobody"]["post"].Responses["415"]; ok {
		t.Error("415 documented for an op without a request body")
	}
	if sec := spec.Paths["/healthz"]["get"].Security; sec == nil || len(sec) != 0 {
		t.Errorf("public op security = %v, want []", sec)
	}
	if _, ok := spec.Components.Schemas["ErrorResponse"]; !ok {
		t.Error("ErrorResponse schema missing")
	}
	if _, ok := spec.Components.Schemas["APIError"]; ok {
		t.Error("APIError leaked into the spec; want ErrorResponse")
	}
	var bad []string
	for name, s := range spec.Components.Schemas {
		walkSchema(name, s, &bad)
	}
	if len(bad) > 0 {
		t.Errorf("undocumented schema fields: %v", bad)
	}
}
