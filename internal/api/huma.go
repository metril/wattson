package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/metril/wattson/internal/auth"
)

// newHumaAPI builds the huma API on r. The docs and spec routes it adds
// inherit whatever middleware r carries.
func newHumaAPI(r chi.Router) huma.API {
	cfg := huma.DefaultConfig("Wattson API", "1.0.0")
	cfg.Info.Description = "REST API of the Wattson server, which controls a Merach exercise bike. " +
		"Authenticate with an API token (`Authorization: Bearer <token>`) or the session cookie " +
		"set by the login flow."
	cfg.CreateHooks = nil // no "$schema" property on responses
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {Type: "http", Scheme: "bearer", Description: "API token from Settings → Tokens"},
		"cookie": {Type: "apiKey", In: "cookie", Name: auth.SessionCookie, Description: "Session cookie set by the login flow."},
	}
	cfg.Security = []map[string][]string{{"bearer": {}}, {"cookie": {}}}
	// APIError is the wire envelope; document it under the public name.
	cfg.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", func(t reflect.Type, hint string) string {
		if t == reflect.TypeFor[APIError]() {
			return "ErrorResponse"
		}
		return huma.DefaultSchemaNamer(t, hint)
	})
	cfg.OpenAPIPath = "/openapi"
	cfg.DocsPath = "/docs"
	return humachi.New(r, cfg)
}

// route describes one typed operation.
type route struct {
	Method, Path, ID, Tag, Summary, Description string
	Scope                                       auth.Scope // "" = public
	Status                                      int        // success status; 0 means 200 (204 without a body)
	Errors                                      []int      // statuses beyond the defaults
}

var jsonBodyMethods = map[string]bool{http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true}

// jsonContentType refuses a request that carries a body unless its
// Content-Type is application/json. A body with no Content-Type (known
// length or chunked) is refused too; a request without a body passes.
func jsonContentType(ctx huma.Context) bool {
	if r, _ := humachi.Unwrap(ctx); r.ContentLength == 0 {
		return true
	}
	mt, _, err := mime.ParseMediaType(ctx.Header("Content-Type"))
	return err == nil && mt == "application/json"
}

// writeRaw writes an error envelope from middleware.
func writeRaw(ctx huma.Context, status int, code, msg string) {
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(
		ErrorResponse{Error: ErrorBody{Code: code, Message: msg}})
}

type loggerKey struct{}

// withLogger makes l available to register's handler wrapper.
func withLogger(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), loggerKey{}, l)))
		})
	}
}

type humaCtxKey struct{}

// httpRequest returns the underlying request of a handler context.
func httpRequest(ctx context.Context) *http.Request {
	hc, ok := ctx.Value(humaCtxKey{}).(huma.Context)
	if !ok {
		return nil
	}
	r, _ := humachi.Unwrap(hc)
	return r
}

// setHeader sets a response header from a handler, including one that
// returns an error (huma writes the status after the handler returns).
func setHeader(ctx context.Context, k, v string) {
	if hc, ok := ctx.Value(humaCtxKey{}).(huma.Context); ok {
		hc.SetHeader(k, v)
	}
}

func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// register adds a typed operation to api. Huma validation stays on; the
// auth middleware maps a missing identity to 401 and a missing scope to 403.
// It panics when the operation lacks an ID, summary, description or tag, so
// an undocumented operation cannot be registered.
//
// Handlers return errors built with apiErr. Any other error, or any status
// of 500 or above (unless built with apiErrPublic), is logged with the operation ID and replaced by a generic
// 500 internal_error, so internals never reach the client.
func register[I, O any](api huma.API, rt route, h func(context.Context, *I) (*O, error)) {
	if rt.ID == "" || rt.Summary == "" || rt.Description == "" || rt.Tag == "" {
		panic(fmt.Sprintf("api: %s %s must have ID, Summary, Description and Tag", rt.Method, rt.Path))
	}
	desc := rt.Description
	if rt.Scope != "" {
		desc += " Requires the `" + string(rt.Scope) + "` scope."
	}
	_, hasBody := reflect.TypeFor[I]().FieldByName("Body")
	jsonBody := hasBody && jsonBodyMethods[rt.Method]
	op := huma.Operation{
		OperationID:   rt.ID,
		Method:        rt.Method,
		Path:          rt.Path,
		Summary:       rt.Summary,
		Description:   desc,
		Tags:          []string{rt.Tag},
		DefaultStatus: rt.Status,
		MaxBodyBytes:  1 << 20,
		Security:      []map[string][]string{},
		Errors:        routeErrors(rt, jsonBody),
	}
	if rt.Scope != "" {
		op.Security = []map[string][]string{{"bearer": {}}, {"cookie": {}}}
	}
	scope := rt.Scope
	op.Middlewares = huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		if scope != "" {
			id, ok := auth.FromContext(ctx.Context())
			if !ok {
				writeRaw(ctx, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			if !id.Has(scope) {
				writeRaw(ctx, http.StatusForbidden, "forbidden", "missing required scope: "+string(scope))
				return
			}
		}
		if jsonBody && !jsonContentType(ctx) {
			writeRaw(ctx, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
			return
		}
		next(huma.WithContext(ctx, context.WithValue(ctx.Context(), humaCtxKey{}, ctx)))
	}}
	huma.Register(api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if err == nil {
			return out, nil
		}
		var se huma.StatusError
		if errors.As(err, &se) && se.GetStatus() < 500 {
			return nil, err
		}
		if ae := (*APIError)(nil); errors.As(err, &ae) && ae.expose {
			return nil, err
		}
		if ctx.Err() != nil { // the client went away mid-request: not a server fault
			loggerFrom(ctx).Debug("handler aborted, client gone", "op", rt.ID, "err", err)
		} else {
			loggerFrom(ctx).Error("handler failed", "op", rt.ID, "err", err)
		}
		return nil, apiErr(http.StatusInternalServerError, "internal_error", "internal error")
	})
}

// routeErrors lists the error statuses an operation documents.
func routeErrors(rt route, hasBody bool) []int {
	errs := map[int]bool{}
	if rt.Scope != "" {
		errs[http.StatusUnauthorized] = true
		errs[http.StatusForbidden] = true
	}
	if strings.Contains(rt.Path, "{") || hasBody {
		errs[http.StatusBadRequest] = true
	}
	if strings.Contains(rt.Path, "{") {
		errs[http.StatusNotFound] = true
	}
	if hasBody {
		errs[http.StatusUnsupportedMediaType] = true
	}
	for _, e := range rt.Errors {
		errs[e] = true
	}
	out := make([]int, 0, len(errs))
	for c := range errs {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}
