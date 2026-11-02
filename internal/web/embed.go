// Package web serves the embedded single-page application.
//
// The Vite build writes into internal/web/dist (see web/vite.config.ts).
// dist/.gitkeep is committed so this package compiles on a clean checkout
// with no Node toolchain; in that state Handler answers 503.
package web

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

//go:embed all:dist
var embedded embed.FS

// Handler serves the embedded SPA.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return notBuilt()
	}
	return handlerFor(sub)
}

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "UI not built", http.StatusServiceUnavailable)
	})
}

// handlerFor builds the SPA handler over any filesystem, so tests can
// supply a synthetic one.
func handlerFor(fsys fs.FS) http.Handler {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return notBuilt()
	}
	files := http.FileServer(http.FS(fsys))
	zipped := &gzCache{fsys: fsys}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if upath == "" || upath == "index.html" {
			serveIndex(w, index)
			return
		}
		info, err := fs.Stat(fsys, upath)
		if err != nil || info.IsDir() {
			// Hashed bundle files must 404 rather than return HTML,
			// otherwise a stale import silently "succeeds". Directories
			// are treated as missing too, so a request like /assets/
			// never falls through to http.FileServer's directory
			// listing.
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, index)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case upath == "sw.js":
			// The service worker script is not hashed: the browser must
			// revalidate it on every check so an update is picked up.
			w.Header().Set("Cache-Control", "no-cache")
		}
		if zipped.serve(w, r, upath) {
			return
		}
		files.ServeHTTP(w, r)
	})
}

// gzExt lists the text formats worth compressing (woff2, images and the like
// are already compressed).
var gzExt = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true, ".json": true}

// gzCache serves gzip-compressed copies of the embedded text files, built once
// on first request. The binary is often the only server in front of the
// browser, and the entry script alone is several times larger than its
// compressed form.
type gzCache struct {
	fsys fs.FS
	m    sync.Map // path -> *gzEntry
}

// gzEntry is compressed once even when the first requests arrive together.
type gzEntry struct {
	once sync.Once
	body []byte // nil: not worth compressing
}

func (c *gzCache) bytes(upath string) []byte {
	v, _ := c.m.LoadOrStore(upath, &gzEntry{})
	e := v.(*gzEntry)
	e.once.Do(func() {
		raw, err := fs.ReadFile(c.fsys, upath)
		if err != nil {
			return
		}
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(raw)
		if zw.Close() == nil && buf.Len() < len(raw) {
			e.body = buf.Bytes()
		}
	})
	return e.body
}

// serve answers GET and HEAD for upath with the compressed copy and reports
// true, when the client accepts gzip and the file is a compressible type.
func (c *gzCache) serve(w http.ResponseWriter, r *http.Request, upath string) bool {
	ext := path.Ext(upath)
	if !gzExt[ext] || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return false
	}
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) || r.Header.Get("Range") != "" {
		return false
	}
	body := c.bytes(upath)
	if body == nil {
		return false
	}
	h := w.Header()
	h.Set("Content-Encoding", "gzip")
	if ct := mime.TypeByExtension(ext); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
	return true
}

// acceptsGzip reports whether an Accept-Encoding value allows gzip (q > 0).
func acceptsGzip(v string) bool {
	for _, part := range strings.Split(v, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(strings.TrimSpace(params), " ", ""), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(index)
}
