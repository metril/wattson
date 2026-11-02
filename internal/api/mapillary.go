package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/mapillary"
	"github.com/metril/wattson/internal/store"
)

// photoFetchTimeout bounds one proxied photo (metadata plus download).
const photoFetchTimeout = 20 * time.Second

// RoutePhotosOut is the street photo state of a route.
type RoutePhotosOut struct {
	Status  string            `json:"status" enum:"disabled,unavailable,building,ready,error" doc:"disabled: no Mapillary token is set. unavailable: this route cannot have photos (see message). building: the photo list is being prepared, ask again shortly. ready: photos holds the list (possibly empty when Mapillary has no coverage). error: the last build failed (see message); it retries after 10 minutes."`
	Message string            `json:"message,omitempty" doc:"Plain-language reason for unavailable and error."`
	BuiltAt *time.Time        `json:"built_at,omitempty" doc:"When the photo list was built, UTC; absent until a build finished."`
	Photos  []mapillary.Photo `json:"photos" doc:"Up to one photo per 15 m of route, sorted by d_m. Empty unless status is ready."`
}

type routePhotosBody struct{ Body RoutePhotosOut }

func (d Deps) registerMapillary(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/routes/{id}/mapillary", ID: "getRoutePhotos", Tag: "mapillary",
		Summary: "Get the street photos along a route",
		Description: "Returns the Mapillary street photos selected along the route: at most one per 15 m, each with its " +
			"distance along the route and camera heading. The list is built in the background the first time it is " +
			"asked for (`status` `building`; poll every few seconds), cached for 30 days and rebuilt when the Mapillary " +
			"token changes. Fetch a photo's image from `GET /api/mapillary/images/{id}`. The token is never returned.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusServiceUnavailable}},
		func(ctx context.Context, in *routeIDIn) (*routePhotosBody, error) {
			if d.Photos == nil {
				return nil, apiErrPublic(http.StatusServiceUnavailable, "mapillary_unavailable", "street photos are not available on this server")
			}
			s, err := d.Photos.Get(ctx, in.ID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				return nil, errNoRoute()
			case errors.Is(err, mapillary.ErrBuilderClosed):
				return nil, apiErrPublic(http.StatusServiceUnavailable, "shutting_down", "the server is shutting down")
			case err != nil:
				return nil, err
			}
			out := RoutePhotosOut{Status: s.State, Message: s.Message, Photos: s.Photos}
			if out.Photos == nil {
				out.Photos = []mapillary.Photo{}
			}
			if !s.BuiltAt.IsZero() {
				t := s.BuiltAt.UTC()
				out.BuiltAt = &t
			}
			return &routePhotosBody{Body: out}, nil
		})
}

func init() {
	registerSpec(func() []opSpec {
		return []opSpec{{
			Method: http.MethodGet, Path: "/api/mapillary/images/{id}", ID: "getMapillaryImage", Tag: "mapillary",
			Summary: "Get a street photo",
			Description: "Streams the 2048 px JPEG of a Mapillary image through the server, so the browser never talks " +
				"to Mapillary and never sees the token. `id` is a photo id from `GET /api/routes/{id}/mapillary` (digits only, " +
				"else 400 `bad_id`). The response is `image/jpeg` of at most 5 MiB, cacheable by the browser for a day, " +
				"with the photographer's Mapillary username in `X-Photo-Creator` (percent-encoded when not plain ASCII). " +
				"Any upstream failure is 502 `photo_unavailable`; 503 `mapillary_unavailable` when the server has no photo support.",
			Params: []p{{Name: "id", Doc: "Mapillary image id, digits only.", Type: "string"}},
			Scope:  auth.ScopeRead, Out: []byte(nil), OutType: "image/jpeg",
			Errors: []int{http.StatusBadGateway, http.StatusServiceUnavailable},
		}}
	})
}

var imageIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// registerMapillaryImages mounts the image proxy (a raw route: it streams bytes).
func (d Deps) registerMapillaryImages(r chi.Router) {
	r.Get("/api/mapillary/images/{id}", d.handleMapillaryImage)
}

func (d Deps) handleMapillaryImage(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.FromContext(r.Context())
	switch {
	case !ok:
		writeEnvelope(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	case !ident.Has(auth.ScopeRead):
		writeEnvelope(w, http.StatusForbidden, "forbidden", "missing required scope: read")
		return
	case d.Mapillary == nil:
		writeEnvelope(w, http.StatusServiceUnavailable, "mapillary_unavailable", "street photos are not available on this server")
		return
	}
	id := chi.URLParam(r, "id")
	if !imageIDRe.MatchString(id) {
		writeEnvelope(w, http.StatusBadRequest, "bad_id", "id: must be 1 to 20 digits")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), photoFetchTimeout)
	defer cancel()
	th, err := d.Mapillary.Thumb(ctx, id)
	if err != nil {
		if r.Context().Err() == nil {
			// The client's errors are scrubbed of the token and carry no photo URL.
			loggerFrom(ctx).Warn("street photo fetch failed", "image", id, "err", err)
		}
		writeEnvelope(w, http.StatusBadGateway, "photo_unavailable", "The photo could not be loaded from Mapillary right now.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Length", strconv.Itoa(len(th.JPEG)))
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	if c := headerSafe(th.Creator); c != "" {
		h.Set("X-Photo-Creator", c)
	}
	_, _ = w.Write(th.JPEG)
}

// headerSafe returns s when it is printable ASCII, else its percent-encoding
// (so a header value never carries control or non-ASCII bytes).
func headerSafe(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e || s[i] == '%' {
			return url.PathEscape(s)
		}
	}
	return s
}
