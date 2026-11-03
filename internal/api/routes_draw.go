package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	wroute "github.com/metril/wattson/internal/route"
)

const (
	brouterDrawTimeout = 30 * time.Second
	maxDrawWaypoints   = 30
	maxTilesInMessage  = 12
)

// DrawIn is the body of POST /api/routes/draw.
type DrawIn struct {
	Body struct {
		Name      string       `json:"name,omitempty" maxLength:"100" doc:"Route name, up to 100 characters. Defaults to Route followed by today's date (YYYY-MM-DD)."`
		Profile   string       `json:"profile,omitempty" enum:"trekking,fastbike,safety,shortest" doc:"BRouter routing profile. Defaults to the profile in Settings → Integrations."`
		Waypoints [][2]float64 `json:"waypoints" minItems:"2" maxItems:"30" doc:"Waypoints in order as [longitude, latitude] pairs in degrees, 2 to 30. BRouter routes along roads between them."`
		DryRun    bool         `json:"dry_run,omitempty" doc:"Compute the route and its profile but do not save it (the returned route has id 0)."`
	}
}

// DrawOut is the result of drawing a route.
type DrawOut struct {
	Route   RouteDetail `json:"route" doc:"The route. With dry_run its id is 0 and nothing was stored."`
	Profile ProfileOut  `json:"profile" doc:"The route's elevation profile; route_id is 0 with dry_run."`
}

type drawResp struct {
	Status int
	Body   DrawOut
}

// missingTilesErr is the 422 for map regions that are not downloaded. The
// names are also in error.details.missing_tiles.
func missingTilesErr(tiles []string) error {
	shown := tiles
	more := ""
	if len(shown) > maxTilesInMessage {
		shown, more = shown[:maxTilesInMessage], fmt.Sprintf(" and %d more", len(tiles)-maxTilesInMessage)
	}
	return apiErrDetails(http.StatusUnprocessableEntity, "missing_tiles",
		"missing map regions: "+strings.Join(shown, ", ")+more+". Download them in Settings → Map regions.",
		&ErrorDetails{MissingTiles: tiles})
}

// brouterClient is the client for BRouter calls: the shared transport without
// its overall timeout (callers bound the call with a context) and without
// following redirects off the configured server.
func (d Deps) brouterClient() *http.Client {
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if d.HTTPClient != nil {
		c.Transport = d.HTTPClient.Transport
	}
	return c
}

// localMissing returns the tiles of the line that the local segments
// directory lacks. It returns nil when segments_dir is empty or does not
// exist (a remote BRouter keeps its own segments) or no tile manager is
// configured. Tiles the segments server does not offer (open sea) are not
// missing; when its index cannot be read, every absent tile counts.
func (d Deps) localMissing(ctx context.Context, wps []wroute.LonLat) ([]string, error) {
	if d.Tiles == nil {
		return nil, nil
	}
	missing, dirExists, err := d.Tiles.Missing(ctx, wroute.TilesForLine(wps))
	if err != nil || !dirExists || len(missing) == 0 {
		return nil, err
	}
	actx, cancel := context.WithTimeout(ctx, availableTimeout)
	defer cancel()
	if avail, err := d.Tiles.Available(actx); err == nil && len(avail) > 0 {
		offered := make(map[string]bool, len(avail))
		for _, a := range avail {
			offered[a] = true
		}
		kept := missing[:0]
		for _, t := range missing {
			if offered[t] {
				kept = append(kept, t)
			}
		}
		missing = kept
	}
	return missing, nil
}

// serverLabel is u without credentials, path and query, safe to show.
func serverLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the configured address"
	}
	return u.Scheme + "://" + u.Host
}

// isTransport reports whether err is a connection-level failure rather than
// an answer the server gave.
func isTransport(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded)
}

// brouterFailure maps a failed BRouter call at base to an API error. The
// cause of anything not shown is logged.
func brouterFailure(ctx context.Context, base string, limit time.Duration, err error) error {
	if ctx.Err() != nil { // the client went away
		return ctx.Err()
	}
	var be *wroute.BRouterError
	if errors.As(err, &be) {
		if be.LookupMismatch {
			return apiErrPublic(http.StatusBadGateway, "brouter_outdated",
				"map regions are a newer format than the routing engine (BRouter); update BRouter")
		}
		return apiErrPublic(http.StatusBadGateway, "brouter_error", "BRouter could not route this: "+be.Message)
	}
	loggerFrom(ctx).Warn("brouter call failed", "server", serverLabel(base), "err", err)
	if errors.Is(err, context.DeadlineExceeded) {
		return apiErrPublic(http.StatusBadGateway, "brouter_unavailable",
			"BRouter at "+serverLabel(base)+" did not answer within "+strconv.FormatFloat(limit.Seconds(), 'f', -1, 64)+" s")
	}
	if isTransport(err) {
		return apiErrPublic(http.StatusBadGateway, "brouter_unavailable", "BRouter is not reachable at "+serverLabel(base))
	}
	return apiErrPublic(http.StatusBadGateway, "brouter_error", "BRouter returned a reply that could not be used.")
}

func (d Deps) registerDraw(api huma.API) {
	register(api, route{Method: http.MethodPost, Path: "/api/routes/draw", ID: "drawRoute", Tag: "routes",
		Summary: "Draw a route with BRouter",
		Description: "Routes between 2 to 30 waypoints with BRouter and, unless `dry_run` is set, stores the result as a route " +
			"owned by the caller (201, source `draw`, the profile and waypoints kept in `spec`). With `dry_run` nothing is stored " +
			"and the response is 200 with route id 0. When a local segments directory exists and lacks map regions the line " +
			"crosses, the request is refused with 422 `missing_tiles` and the tile names in `error.details.missing_tiles` " +
			"(download them under Map regions), unless the admin enabled the public BRouter fallback in Settings, in which case the " +
			"waypoints are sent to that public server instead. Other errors: 400 for coordinates out of range; " +
			"422 `route_too_short`, `route_too_long` or `no_elevation`; 502 `brouter_unavailable` (BRouter cannot be reached), " +
			"`brouter_outdated` (the map regions are newer than BRouter), `brouter_error` (BRouter refused the route) or `elevation_unavailable`.",
		Scope: auth.ScopeControl, Status: http.StatusCreated, AlsoOK: []int{http.StatusOK},
		Errors: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}},
		func(ctx context.Context, in *DrawIn) (*drawResp, error) {
			ident, _ := auth.FromContext(ctx)
			st, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			profile := in.Body.Profile
			if profile == "" {
				profile = st.BRouter.Profile
			}
			wps := make([]wroute.LonLat, len(in.Body.Waypoints))
			for i, w := range in.Body.Waypoints {
				if !(w[0] >= -180 && w[0] <= 180 && w[1] >= -90 && w[1] <= 90) {
					return nil, apiErr(http.StatusBadRequest, "invalid_request",
						fmt.Sprintf("waypoints[%d]: longitude must be within -180..180 and latitude within -90..90 ([lon, lat])", i))
				}
				wps[i] = wroute.LonLat(w)
			}

			limit := d.drawTimeout
			if limit == 0 {
				limit = brouterDrawTimeout
			}
			base, fellBack := st.BRouter.URL, false
			missing, err := d.localMissing(ctx, wps)
			if err != nil {
				return nil, err
			}
			if len(missing) > 0 {
				if !st.BRouter.PublicFallback {
					return nil, missingTilesErr(missing)
				}
				base, fellBack = st.BRouter.PublicURL, true
			}
			var pts []wroute.Point
			for {
				cctx, cancel := context.WithTimeout(ctx, limit)
				pts, err = wroute.BRouter{BaseURL: base, Client: d.brouterClient()}.Route(cctx, wps, profile)
				cancel()
				if err == nil {
					break
				}
				var mt *wroute.MissingTilesError
				if !fellBack && errors.As(err, &mt) && ctx.Err() == nil {
					if !st.BRouter.PublicFallback {
						return nil, missingTilesErr(mt.Tiles)
					}
					base, fellBack = st.BRouter.PublicURL, true
					continue
				}
				return nil, brouterFailure(ctx, base, limit, err)
			}

			p, err := d.buildRoute(ctx, pts)
			if err != nil {
				return nil, routeErr(ctx, err)
			}
			name := cleanName(in.Body.Name)
			if name == "" {
				name = "Route " + d.Now().Format("2006-01-02")
			}
			spec := RouteSpec{Profile: profile, Waypoints: in.Body.Waypoints}
			if in.Body.DryRun {
				rec, err := d.routeRecord(ident.User.ID, name, "draw", spec, p)
				if err != nil {
					return nil, err
				}
				det, err := routeDetail(rec)
				if err != nil {
					return nil, err
				}
				return &drawResp{Status: http.StatusOK, Body: DrawOut{Route: det, Profile: profileOut(0, p)}}, nil
			}
			rec, err := d.saveRoute(ctx, ident.User.ID, name, "draw", spec, p)
			if err != nil {
				return nil, err
			}
			det, err := routeDetail(rec)
			if err != nil {
				return nil, err
			}
			return &drawResp{Status: http.StatusCreated, Body: DrawOut{Route: det, Profile: profileOut(rec.ID, p)}}, nil
		})
}
