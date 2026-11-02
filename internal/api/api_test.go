package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

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
		"Content-Security-Policy": "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'",
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

func TestSPAServedForUnknownPaths(t *testing.T) {
	t.Parallel()
	ts := newTS(t)
	// dist is unbuilt in tests: the web handler answers (503) rather than JSON 404.
	resp, _ := ts.do(t, nil, "GET", "/riders", nil)
	if resp.StatusCode != 503 && resp.StatusCode != 200 {
		t.Errorf("/riders: %d", resp.StatusCode)
	}
}

func TestTypedOperationSpec(t *testing.T) {
	t.Parallel()
	ts := newTS(t, withOps(registerPing))
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
