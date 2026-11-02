package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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

func TestEmbeddedHandler(t *testing.T) {
	t.Parallel()
	// A clean checkout only has dist/.gitkeep (503); after a UI build the
	// embedded index.html is served (200).
	want := http.StatusServiceUnavailable
	if _, err := os.Stat("dist/index.html"); err == nil {
		want = http.StatusOK
	}
	if rec := get(Handler(), "/"); rec.Code != want {
		t.Errorf("Handler /: %d, want %d", rec.Code, want)
	}
}

func TestSWNoCache(t *testing.T) {
	t.Parallel()
	h := handlerFor(fstest.MapFS{
		"index.html": {Data: []byte("<html>app</html>")},
		"sw.js":      {Data: []byte("self.skipWaiting()")},
	})
	rec := get(h, "/sw.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "self.skipWaiting()" {
		t.Fatalf("/sw.js: %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestGzipAssets(t *testing.T) {
	t.Parallel()
	js := strings.Repeat("console.log('wattson');\n", 400)
	h := handlerFor(fstest.MapFS{
		"index.html":        {Data: []byte("<html>app</html>")},
		"assets/a.js":       {Data: []byte(js)},
		"assets/font.woff2": {Data: []byte(js)}, // already compressed formats are left alone
		"sw.js":             {Data: []byte(js)},
	})
	do := func(path, enc string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if enc != "" {
			req.Header.Set("Accept-Encoding", enc)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := do("/assets/a.js", "br, gzip;q=0.8")
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers = %v, want gzip and Vary", rec.Header())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != js {
		t.Errorf("decompressed body differs (%d bytes)", len(got))
	}
	if n, _ := strconv.Atoi(rec.Header().Get("Content-Length")); n == 0 || n >= len(js)/4 {
		t.Errorf("Content-Length = %d, want a small compressed length", n)
	}

	if rec := do("/assets/a.js", ""); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != js || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("no Accept-Encoding: %v, %d bytes", rec.Header(), rec.Body.Len())
	}
	if rec := do("/assets/a.js", "gzip;q=0"); rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("gzip;q=0 must not be gzipped")
	}
	if rec := do("/assets/font.woff2", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("woff2 gzipped")
	}
	if rec := do("/sw.js", "gzip"); rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("sw.js: %v", rec.Header())
	}
	// A Range request is answered from the uncompressed file (ranges of a gzip stream are meaningless).
	rreq := httptest.NewRequest(http.MethodGet, "/assets/a.js", nil)
	rreq.Header.Set("Accept-Encoding", "gzip")
	rreq.Header.Set("Range", "bytes=0-9")
	rrec := httptest.NewRecorder()
	h.ServeHTTP(rrec, rreq)
	if rrec.Code != http.StatusPartialContent || rrec.Header().Get("Content-Encoding") != "" || rrec.Body.String() != js[:10] {
		t.Errorf("Range: %d %v %q", rrec.Code, rrec.Header(), rrec.Body.String())
	}
	// An explicit /index.html is the SPA entry: never cached, never a stale copy.
	if rec := do("/index.html", "gzip"); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" || rec.Body.String() != "<html>app</html>" {
		t.Errorf("/index.html: %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}
	head := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/assets/a.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(head, req)
	if head.Body.Len() != 0 || head.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("HEAD: %v, %d body bytes", head.Header(), head.Body.Len())
	}
}
