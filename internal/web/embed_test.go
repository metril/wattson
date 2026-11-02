package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestSPAFallback(t *testing.T) {
	t.Parallel()
	h := handlerFor(fstest.MapFS{
		"index.html":  {Data: []byte("<html>app</html>")},
		"assets/a.js": {Data: []byte("console.log(1)")},
	})

	if rec := get(h, "/riders"); rec.Code != http.StatusOK || rec.Body.String() != "<html>app</html>" {
		t.Errorf("/riders: %d %q, want index.html", rec.Code, rec.Body.String())
	}
	if rec := get(h, "/"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("/: %d cache=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Errorf("/assets/missing.js: %d, want 404", rec.Code)
	}
	if rec := get(h, "/assets/"); rec.Code != http.StatusNotFound {
		t.Errorf("/assets/: %d, want 404", rec.Code)
	}
	rec := get(h, "/assets/a.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("/assets/a.js: %d", rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

func TestUINotBuilt(t *testing.T) {
	t.Parallel()
	rec := get(handlerFor(fstest.MapFS{}), "/")
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "UI not built\n" {
		t.Errorf("empty dist: %d %q, want 503 \"UI not built\"", rec.Code, rec.Body.String())
	}
}

func TestEmbeddedHandlerWithoutBuild(t *testing.T) {
	t.Parallel()
	// A clean checkout only has dist/.gitkeep, so Handler answers 503.
	if rec := get(Handler(), "/"); rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusOK {
		t.Errorf("Handler /: %d", rec.Code)
	}
}
