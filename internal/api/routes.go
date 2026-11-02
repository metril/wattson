package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	wroute "github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/store"
)

// MaxImportBytes bounds one route file upload.
const MaxImportBytes = 20 << 20

const (
	maxRouteNameRunes = 100
	maxSyntheticM     = wroute.MaxLengthM
)

// RouteOut summarises a stored route.
type RouteOut struct {
	ID          int64     `json:"id" doc:"Route ID."`
	Name        string    `json:"name" doc:"Display name of the route."`
	Source      string    `json:"source" enum:"gpx,tcx,fit,draw,synthetic" doc:"How the route was made: an imported gpx, tcx or fit file, drawn on the map, or built from synthetic segments."`
	OwnerID     int64     `json:"owner_id" doc:"ID of the user who added the route; 0 when that user has since been deleted."`
	DistanceM   float64   `json:"distance_m" doc:"Route length in metres."`
	AscentM     float64   `json:"ascent_m" doc:"Total climb in metres."`
	DescentM    float64   `json:"descent_m" doc:"Total descent in metres."`
	MinGradePct float64   `json:"min_grade_pct" doc:"Steepest descent as a percent grade (negative or zero)."`
	MaxGradePct float64   `json:"max_grade_pct" doc:"Steepest climb as a percent grade."`
	CreatedAt   time.Time `json:"created_at" doc:"Creation time, UTC."`
}

// SegmentIn is one stretch of a synthetic route.
type SegmentIn struct {
	LengthM  float64 `json:"length_m" minimum:"10" maximum:"100000" doc:"Segment length in metres, 10 to 100000."`
	GradePct float64 `json:"grade_pct" minimum:"-25" maximum:"25" doc:"Constant grade of the segment in percent, -25 to 25."`
}

// RouteSpec records how a route was built, so it can be edited again.
type RouteSpec struct {
	Profile   string       `json:"profile,omitempty" doc:"Routing profile used to draw the route (drawn routes)."`
	Waypoints [][2]float64 `json:"waypoints,omitempty" doc:"Waypoints as [longitude, latitude] pairs (drawn routes)."`
	Segments  []SegmentIn  `json:"segments,omitempty" doc:"Segments the route was built from (synthetic routes)."`
}

// LineStringOut is a GeoJSON LineString geometry.
type LineStringOut struct {
	Type        string       `json:"type" enum:"LineString" doc:"Always LineString."`
	Coordinates [][2]float64 `json:"coordinates" doc:"Track as [longitude, latitude] pairs in degrees, simplified to about 2 metres."`
}

// RouteDetail is a route with its geometry and spec.
type RouteDetail struct {
	RouteOut
	BBox    [4]float64    `json:"bbox" doc:"Bounding box as [west, south, east, north] in degrees."`
	GeoJSON LineStringOut `json:"geojson" doc:"The route as a GeoJSON LineString."`
	Spec    RouteSpec     `json:"spec" doc:"How the route was built; empty for imported files."`
}

// ProfileOut is the elevation profile, resampled at a fixed step.
type ProfileOut struct {
	RouteID  int64     `json:"route_id" doc:"Route ID."`
	StepM    float64   `json:"step_m" doc:"Distance between samples in metres."`
	LengthM  float64   `json:"length_m" doc:"Route length in metres; the last sample may be closer than step_m to the one before."`
	Lat      []float64 `json:"lat" doc:"Latitude of each sample in degrees, 6 decimals."`
	Lon      []float64 `json:"lon" doc:"Longitude of each sample in degrees, 6 decimals."`
	EleM     []float64 `json:"ele_m" doc:"Smoothed elevation of each sample in metres, 1 decimal."`
	GradePct []float64 `json:"grade_pct" doc:"Road grade at each sample in percent, 2 decimals."`
}

func routeOut(r store.Route) RouteOut {
	return RouteOut{ID: r.ID, Name: r.Name, Source: r.Source, OwnerID: r.UserID, DistanceM: r.DistanceM,
		AscentM: r.AscentM, DescentM: r.DescentM, MinGradePct: r.MinGradePct, MaxGradePct: r.MaxGradePct,
		CreatedAt: r.CreatedAt}
}

func routeDetail(r store.Route) (RouteDetail, error) {
	d := RouteDetail{RouteOut: routeOut(r), BBox: r.BBox}
	if err := json.Unmarshal(r.GeoJSON, &d.GeoJSON); err != nil {
		return RouteDetail{}, err
	}
	if len(r.Spec) > 0 {
		if err := json.Unmarshal(r.Spec, &d.Spec); err != nil {
			return RouteDetail{}, err
		}
	}
	return d, nil
}

func round(v float64, decimals int) float64 {
	p := math.Pow10(decimals)
	return math.Round(v*p) / p
}

func roundAll(xs []float64, decimals int) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = round(x, decimals)
	}
	return out
}

// buildRoute fills missing elevation (OpenTopoData, only when enabled in
// the settings) and resamples pts into a Profile. Failures are the route
// package's errors; routeErr maps them to API errors.
func (d Deps) buildRoute(ctx context.Context, pts []wroute.Point) (*wroute.Profile, error) {
	st, err := d.Settings.Get(ctx)
	if err != nil {
		return nil, err
	}
	var f wroute.ElevationFiller
	if st.Elevation.OpenTopoData {
		f = wroute.OpenTopoData{BaseURL: st.Elevation.OpenTopoDataURL, Client: d.HTTPClient, Interval: time.Second}
	}
	if err := wroute.PrepareElevation(ctx, pts, f); err != nil {
		return nil, err
	}
	return wroute.Process(pts)
}

// routeRecord builds the unsaved route (ID 0) for p.
func (d Deps) routeRecord(userID int64, name, source string, spec RouteSpec, p *wroute.Profile) (store.Route, error) {
	geo, err := json.Marshal(wroute.GeoJSON(p))
	if err != nil {
		return store.Route{}, err
	}
	sp, err := json.Marshal(spec)
	if err != nil {
		return store.Route{}, err
	}
	s := p.Stats()
	return store.Route{UserID: userID, Name: name, Source: source,
		DistanceM: s.DistanceM, AscentM: s.AscentM, DescentM: s.DescentM, MinGradePct: s.MinGradePct,
		MaxGradePct: s.MaxGradePct, BBox: s.BBox, GeoJSON: geo, Spec: sp, Points: wroute.Encode(p),
		CreatedAt: d.Now().UTC()}, nil
}

// saveRoute stores p as a new route owned by userID.
func (d Deps) saveRoute(ctx context.Context, userID int64, name, source string, spec RouteSpec, p *wroute.Profile) (store.Route, error) {
	r, err := d.routeRecord(userID, name, source, spec, p)
	if err != nil {
		return store.Route{}, err
	}
	return d.Store.CreateRoute(ctx, r)
}

func profileOut(routeID int64, p *wroute.Profile) ProfileOut {
	return ProfileOut{RouteID: routeID, StepM: p.StepM, LengthM: round(p.LengthM, 2),
		Lat: roundAll(p.Lat, 6), Lon: roundAll(p.Lon, 6), EleM: roundAll(p.Ele, 1), GradePct: roundAll(p.Grade, 2)}
}

// routeErr maps a route package failure to an API error. Anything
// unrecognised is returned unchanged (register turns it into a 500).
//
// The elevation service being unreachable is a gateway failure of a service
// the admin chose to enable, so it is a public 502 elevation_unavailable
// (register would otherwise mask every 5xx); the cause is only logged.
func routeErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, wroute.ErrUnsupportedFormat):
		return apiErr(http.StatusBadRequest, "unsupported_format", "Unsupported file type. Upload a GPX, TCX or FIT file.")
	case errors.Is(err, wroute.ErrNoTrack):
		return apiErr(http.StatusBadRequest, "no_track", "The file contains no track points.")
	case errors.Is(err, wroute.ErrTooLarge):
		return apiErr(http.StatusRequestEntityTooLarge, "too_large", "The file is too large.")
	case errors.Is(err, wroute.ErrTooManyPoints):
		return apiErr(http.StatusUnprocessableEntity, "too_many_points", "The file has too many track points.")
	case errors.Is(err, wroute.ErrNoElevation):
		return apiErr(http.StatusUnprocessableEntity, "no_elevation",
			"No elevation in this file. Enable OpenTopoData elevation in Settings → Integrations.")
	case errors.Is(err, wroute.ErrTooShort):
		return apiErr(http.StatusUnprocessableEntity, "route_too_short", "The route is shorter than 50 m.")
	case errors.Is(err, wroute.ErrTooLong):
		return apiErr(http.StatusUnprocessableEntity, "route_too_long", "The route is longer than 500 km.")
	case errors.Is(err, wroute.ErrElevationUnavailable):
		if ctx.Err() != nil { // the client went away: not a service fault
			loggerFrom(ctx).Debug("elevation lookup aborted, client gone", "err", err)
			return ctx.Err()
		}
		loggerFrom(ctx).Warn("elevation lookup failed", "err", err)
		return apiErrPublic(http.StatusBadGateway, "elevation_unavailable",
			"The elevation service could not be reached. Try again later or disable OpenTopoData in Settings → Integrations.")
	}
	return err
}

// cleanName trims name and cuts it to the maximum length.
func cleanName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxRouteNameRunes {
		name = strings.TrimSpace(string([]rune(name)[:maxRouteNameRunes]))
	}
	return name
}

func errNoRoute() error { return apiErr(http.StatusNotFound, "not_found", "no such route") }

type routeIDIn struct {
	ID int64 `path:"id" doc:"Route ID."`
}

type routesOut struct {
	Body []RouteOut
}

type routeDetailOut struct {
	Body RouteDetail
}

type profileBody struct {
	Body ProfileOut
}

type patchRouteIn struct {
	ID   int64 `path:"id" doc:"Route ID."`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100" doc:"New display name, 1 to 100 characters."`
	}
}

type importRouteIn struct {
	Name    string `query:"name" maxLength:"100" doc:"Route name. Defaults to the name stored in the file, else the file name without its extension."`
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" required:"true" doc:"The route file: GPX, TCX or FIT, at most 20 MiB. The format is detected from the content, then the file name."`
	}]
}

type syntheticIn struct {
	Body struct {
		Name     string      `json:"name" minLength:"1" maxLength:"100" doc:"Route name, 1 to 100 characters."`
		Segments []SegmentIn `json:"segments" minItems:"1" maxItems:"100" doc:"Segments ridden in order, 1 to 100. The total length must not exceed 500 km."`
	}
}

// authorize loads route id and refuses callers who neither own it nor are admins.
func (d Deps) authorize(ctx context.Context, id int64) (store.Route, error) {
	ident, _ := auth.FromContext(ctx)
	r, err := d.Store.Route(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Route{}, errNoRoute()
	}
	if err != nil {
		return store.Route{}, err
	}
	if !ident.Has(auth.ScopeAdmin) && (r.UserID == 0 || r.UserID != ident.User.ID) {
		return store.Route{}, apiErr(http.StatusForbidden, "not_owner", "only the owner or an admin can change this route")
	}
	return r, nil
}

func (d Deps) registerRoutes(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/routes", ID: "listRoutes", Tag: "routes",
		Summary:     "List routes",
		Description: "Returns every route, newest first, without geometry. Routes are shared: every signed-in user sees all of them.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, _ *struct{}) (*routesOut, error) {
			rs, err := d.Store.ListRoutes(ctx)
			if err != nil {
				return nil, err
			}
			out := &routesOut{Body: make([]RouteOut, 0, len(rs))}
			for _, r := range rs {
				out.Body = append(out.Body, routeOut(r))
			}
			return out, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/routes/{id}", ID: "getRoute", Tag: "routes",
		Summary:     "Get a route",
		Description: "Returns a route with its bounding box, simplified GeoJSON line and spec. Any signed-in user may read any route.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, in *routeIDIn) (*routeDetailOut, error) {
			r, err := d.Store.Route(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRoute()
			}
			if err != nil {
				return nil, err
			}
			det, err := routeDetail(r)
			if err != nil {
				return nil, err
			}
			return &routeDetailOut{Body: det}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/routes/{id}/profile", ID: "getRouteProfile", Tag: "routes",
		Summary: "Get a route's elevation profile",
		Description: "Returns the route resampled every `step_m` metres as parallel arrays (latitude, longitude, " +
			"elevation, grade). The response is gzip-compressed when the client sends `Accept-Encoding: gzip`.",
		Scope: auth.ScopeRead},
		func(ctx context.Context, in *routeIDIn) (*profileBody, error) {
			r, err := d.Store.Route(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRoute()
			}
			if err != nil {
				return nil, err
			}
			p, err := wroute.Decode(r.Points)
			if err != nil {
				return nil, err
			}
			return &profileBody{Body: profileOut(r.ID, p)}, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/routes/{id}", ID: "renameRoute", Tag: "routes",
		Summary:     "Rename a route",
		Description: "Changes the route's name. Only the route's owner or an admin may do this; anyone else gets 403 `not_owner`.",
		Scope:       auth.ScopeControl},
		func(ctx context.Context, in *patchRouteIn) (*routeDetailOut, error) {
			if _, err := d.authorize(ctx, in.ID); err != nil {
				return nil, err
			}
			name := cleanName(in.Body.Name)
			if name == "" {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "name: must not be blank")
			}
			r, err := d.Store.RenameRoute(ctx, in.ID, name)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRoute()
			}
			if err != nil {
				return nil, err
			}
			det, err := routeDetail(r)
			if err != nil {
				return nil, err
			}
			return &routeDetailOut{Body: det}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/routes/{id}", ID: "deleteRoute", Tag: "routes",
		Summary: "Delete a route",
		Description: "Deletes a route. Only the route's owner or an admin may do this (403 `not_owner` otherwise). " +
			"A route the connected ride is using cannot be deleted (409 `route_in_use`); stop the ride first.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *routeIDIn) (*noContent, error) {
			if _, err := d.authorize(ctx, in.ID); err != nil {
				return nil, err
			}
			if d.RouteInUse != nil && d.RouteInUse(in.ID) {
				return nil, apiErr(http.StatusConflict, "route_in_use", "the route is in use by the current ride; stop the ride first")
			}
			if err := d.Store.DeleteRoute(ctx, in.ID); errors.Is(err, store.ErrNotFound) {
				return nil, errNoRoute()
			} else if err != nil {
				return nil, err
			}
			return &noContent{}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/routes/import", ID: "importRoute", Tag: "routes",
		Summary: "Import a route file",
		Description: "Uploads a GPX, TCX or FIT file (multipart/form-data, field `file`, at most 20 MiB) and stores it as a route " +
			"owned by the caller. Tracks without elevation, or above 60 degrees latitude, are looked up at OpenTopoData when " +
			"that is enabled in Settings. Errors: 400 `unsupported_format`, `no_track` or `invalid_file` (the file could not be parsed); 413 `too_large`; " +
			"422 `no_elevation` (enable OpenTopoData elevation in Settings → Integrations), `route_too_short` (under 50 m), " +
			"`route_too_long` (over 500 km) or `too_many_points`; 502 `elevation_unavailable` (the elevation service failed).",
		Scope: auth.ScopeControl, Status: http.StatusCreated, Multipart: true, MaxBody: MaxImportBytes, ReadTimeout: 2 * time.Minute,
		Errors: []int{http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity, http.StatusBadGateway}},
		func(ctx context.Context, in *importRouteIn) (*routeDetailOut, error) {
			ident, _ := auth.FromContext(ctx)
			f := in.RawBody.Data().File
			defer f.Close()
			br := bufio.NewReaderSize(f, 8192)
			head, _ := br.Peek(8192)
			format, err := wroute.Detect(f.Filename, head)
			if err != nil {
				return nil, routeErr(ctx, err)
			}
			pts, fileName, err := wroute.Parse(format, br)
			if err != nil {
				if errors.Is(err, wroute.ErrNoTrack) || errors.Is(err, wroute.ErrTooLarge) || errors.Is(err, wroute.ErrTooManyPoints) {
					return nil, routeErr(ctx, err)
				}
				return nil, apiErr(http.StatusBadRequest, "invalid_file", "The file could not be read as a "+strings.ToUpper(string(format))+" route.")
			}
			name := cleanName(in.Name)
			if name == "" {
				name = cleanName(fileName)
			}
			if name == "" {
				ext := filepath.Ext(f.Filename)
				name = cleanName(strings.TrimSuffix(filepath.Base(f.Filename), ext))
			}
			if name == "" {
				name = "Imported route"
			}
			p, err := d.buildRoute(ctx, pts)
			if err != nil {
				return nil, routeErr(ctx, err)
			}
			r, err := d.saveRoute(ctx, ident.User.ID, name, string(format), RouteSpec{}, p)
			if err != nil {
				return nil, err
			}
			det, err := routeDetail(r)
			if err != nil {
				return nil, err
			}
			return &routeDetailOut{Body: det}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/routes/synthetic", ID: "createSyntheticRoute", Tag: "routes",
		Summary: "Build a synthetic route",
		Description: "Creates a route from constant-grade segments ridden in order, for example to train a specific climb. " +
			"The route starts at 100 m elevation. The total length may not exceed 500 km (422 `route_too_long`).",
		Scope: auth.ScopeControl, Status: http.StatusCreated, Errors: []int{http.StatusUnprocessableEntity}},
		func(ctx context.Context, in *syntheticIn) (*routeDetailOut, error) {
			ident, _ := auth.FromContext(ctx)
			name := cleanName(in.Body.Name)
			if name == "" {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "name: must not be blank")
			}
			segs := make([]wroute.Segment, len(in.Body.Segments))
			var total float64
			for i, s := range in.Body.Segments {
				segs[i] = wroute.Segment{LengthM: s.LengthM, GradePct: s.GradePct}
				total += s.LengthM
			}
			if total > maxSyntheticM {
				return nil, routeErr(ctx, wroute.ErrTooLong)
			}
			p, err := d.buildRoute(ctx, wroute.Synthetic(segs))
			if err != nil {
				return nil, routeErr(ctx, err)
			}
			r, err := d.saveRoute(ctx, ident.User.ID, name, "synthetic", RouteSpec{Segments: in.Body.Segments}, p)
			if err != nil {
				return nil, err
			}
			det, err := routeDetail(r)
			if err != nil {
				return nil, err
			}
			return &routeDetailOut{Body: det}, nil
		})
}
