// Package web serves the embedded single-page application.
//
// The Vite build writes into internal/web/dist (see web/vite.config.ts).
// dist/.gitkeep is committed so this package compiles on a clean checkout
// with no Node toolchain; in that state Handler answers 503.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := strings.TrimPrefix(r.URL.Path, "/")
		if upath == "" {
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
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(index)
}
