// Package api serves the Wattson HTTP API: typed huma operations under
// /api, raw routes, the OpenAPI document and the embedded web UI.
package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/maptiles"
	"github.com/metril/wattson/internal/oidcauth"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/web"
)

// Deps are the server's collaborators. Later tasks add their own fields
// (OIDC, Bike, WSRecheck) so every commit compiles.
type Deps struct {
	Store      *store.Store
	Settings   *settings.Service
	Box        *secretbox.Box
	Auth       *auth.Authenticator
	OIDC       *oidcauth.Registry // discovered OIDC providers; NewServer creates one when nil
	HTTPClient *http.Client       // outbound calls (HA ping, OIDC); tests inject
	Logger     *slog.Logger
	Now        func() time.Time
	Version    string
	// RiderInUse reports whether a rider is bound to the active bike
	// connection (ridectl.Manager.RiderInUse); deleting that rider is refused
	// with 409 rider_in_use. Nil means no rider is ever in use.
	RiderInUse func(riderID int64) bool
	// RouteInUse reports whether a route is in use by the active ride;
	// deleting that route is refused with 409 route_in_use. Nil means no
	// route is ever in use.
	RouteInUse func(routeID int64) bool
	// Tiles downloads and lists BRouter map regions (segments). Nil disables
	// the /api/maps endpoints (503) and the local tile check when drawing.
	Tiles *maptiles.Manager
	// Bike is the single bike manager the bike, ride and WebSocket endpoints
	// drive. When RiderInUse or RouteInUse is nil, the Bike's is used.
	Bike *ridectl.Manager
	// WSRecheck is how often an open WebSocket re-validates its credential;
	// zero means 60 s.
	WSRecheck time.Duration

	drawTimeout time.Duration // BRouter draw call limit; zero means 30 s (tests shorten it)

	ws        *wsGate              // open WebSocket count; set by newRouter
	oidcState *oidcauth.StateCodec // signs the OIDC state cookie; derived from Box
}

// NewServer builds the full HTTP handler (an error if Deps.Box is nil): Recoverer, security headers and
// the auth middleware around /healthz, the huma API (/api/*, /docs,
// /openapi.json) and, for everything else, the embedded web UI.
func NewServer(d Deps) (http.Handler, error) { return newRouter(d) }

// newRouter is NewServer plus extra huma registrations (tests only).
func newRouter(d Deps, extra ...func(huma.API)) (http.Handler, error) {
	if d.Box == nil {
		return nil, errors.New("api: Deps.Box is required (it signs the OIDC state and seals secrets)")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.OIDC == nil {
		d.OIDC = oidcauth.NewRegistry(d.HTTPClient)
	}
	if d.RiderInUse == nil && d.Bike != nil {
		d.RiderInUse = d.Bike.RiderInUse
	}
	if d.RouteInUse == nil && d.Bike != nil {
		d.RouteInUse = d.Bike.RouteInUse
	}
	d.ws = &wsGate{}
	d.oidcState = newStateCodec(d)
	r := chi.NewRouter()
	r.Use(d.recoverer, securityHeaders, apiNoStore, middleware.Compress(5, "application/json"), withLogger(d.Logger))
	if d.Auth != nil {
		r.Use(d.Auth.Middleware)
	}
	api := newHumaAPI(r)
	d.registerAPI(api)
	for _, f := range extra {
		f(api)
	}
	addDocsOnly(api)
	finalizeSpec(api)
	r.Get("/healthz", d.healthz)
	r.Get("/ws", d.serveWS)
	d.registerOIDCFlow(r)

	spa := web.Handler()
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api" || strings.HasPrefix(req.URL.Path, "/api/") {
			writeEnvelope(w, http.StatusNotFound, "not_found", "no such endpoint")
			return
		}
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeEnvelope(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		spa.ServeHTTP(w, req)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	})
	return r, nil
}

const csp = "default-src 'self'; connect-src 'self' https://tiles.openfreemap.org; img-src 'self' data: blob: https://tiles.openfreemap.org; " +
	"worker-src 'self' blob:; child-src blob:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		// huma's /docs handler replaces this with its own CSP, which allows
		// only the pinned Stoplight Elements files from unpkg.com.
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// apiNoStore marks every /api response as private and uncacheable: bodies
// are per-user (rides, FIT files, links), so no shared cache may keep them.
func apiNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// trackingWriter records whether the response has started, so the
// recoverer never writes a second status line or envelope.
type trackingWriter struct {
	http.ResponseWriter
	started bool
}

func (t *trackingWriter) WriteHeader(code int) {
	t.started = true
	t.ResponseWriter.WriteHeader(code)
}

func (t *trackingWriter) Write(b []byte) (int, error) {
	t.started = true
	return t.ResponseWriter.Write(b)
}

func (t *trackingWriter) Flush() {
	t.started = true
	http.NewResponseController(t.ResponseWriter).Flush()
}

func (t *trackingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	t.started = true
	return http.NewResponseController(t.ResponseWriter).Hijack()
}

func (t *trackingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// recoverer turns a handler panic into a logged 500 error envelope, unless
// the response has already started.
func (d Deps) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &trackingWriter{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			d.Logger.Error("panic serving request", "method", r.Method, "path", r.URL.Path,
				"panic", rec, "stack", string(debug.Stack()))
			if !tw.started {
				writeEnvelope(w, http.StatusInternalServerError, "internal_error", "internal error")
			}
		}()
		next.ServeHTTP(tw, r)
	})
}

// writeEnvelope writes the standard JSON error body.
func writeEnvelope(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: ErrorBody{Code: code, Message: msg}})
}
