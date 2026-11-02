package api

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/integrations/fitexport"
	"github.com/metril/wattson/internal/store"
)

// fitMediaType is the media type of a FIT file download.
const fitMediaType = "application/vnd.ant.fit"

// StravaStatusOut is a ride's Strava upload progress.
type StravaStatusOut struct {
	Status      string `json:"status" enum:"none,uploading,done,error" doc:"Upload state: none (never uploaded), uploading, done or error."`
	UploadID    int64  `json:"upload_id" doc:"Strava upload ID while uploading or done; 0 when there is none."`
	ActivityID  int64  `json:"activity_id" doc:"Strava activity ID once done; 0 when there is none."`
	ActivityURL string `json:"activity_url" doc:"Link to the Strava activity once done; empty otherwise."`
	Error       string `json:"error" doc:"Why the last upload failed; empty unless status is error."`
}

// RideOut is a recorded ride and its summary.
type RideOut struct {
	ID            int64           `json:"id" doc:"Ride ID."`
	RiderID       int64           `json:"rider_id" doc:"ID of the rider who rode."`
	RiderName     string          `json:"rider_name" doc:"Name of the rider."`
	RouteID       int64           `json:"route_id" doc:"ID of the route ridden; 0 for a free ride or when the route has been deleted."`
	RouteName     string          `json:"route_name" doc:"Name of the route when the ride started; empty for a free ride."`
	Mode          string          `json:"mode" doc:"Ride mode, such as free or route."`
	StartedAt     time.Time       `json:"started_at" doc:"Start time, UTC."`
	EndedAt       *time.Time      `json:"ended_at" nullable:"true" doc:"End time, UTC; null while the ride is still recording."`
	DurationS     float64         `json:"duration_s" doc:"Moving time in seconds, excluding pauses."`
	ElapsedS      float64         `json:"elapsed_s" doc:"Riding time of the last sample in seconds, as the ride clock counted it."`
	DistanceM     float64         `json:"distance_m" doc:"Distance ridden in metres."`
	AscentM       float64         `json:"ascent_m" doc:"Total climbing in metres; 0 on a free ride."`
	AvgPowerW     float64         `json:"avg_power_w" doc:"Average power in watts."`
	MaxPowerW     float64         `json:"max_power_w" doc:"Highest power of one sample in watts."`
	NPW           float64         `json:"np_w" doc:"Normalized power in watts (30 s rolling average, fourth-power mean)."`
	KJ            float64         `json:"kj" doc:"Work done in kilojoules."`
	AvgCadenceRpm float64         `json:"avg_cadence_rpm" doc:"Average cadence in revolutions per minute."`
	AvgHRBpm      int             `json:"avg_hr_bpm" doc:"Average heart rate in beats per minute; 0 when no heart rate was recorded."`
	MaxHRBpm      int             `json:"max_hr_bpm" doc:"Highest heart rate in beats per minute; 0 when no heart rate was recorded."`
	AvgSpeedKmh   float64         `json:"avg_speed_kmh" doc:"Average speed in kilometres per hour."`
	MaxSpeedKmh   float64         `json:"max_speed_kmh" doc:"Highest speed in kilometres per hour."`
	SampleCount   int             `json:"sample_count" doc:"Number of stored 1 Hz samples."`
	Strava        StravaStatusOut `json:"strava" doc:"Strava upload state of the ride."`
}

func rideSummary(r store.Ride) RideOut {
	s := r.Summary
	o := RideOut{ID: r.ID, RiderID: r.RiderID, RiderName: r.RiderName, RouteID: r.RouteID, RouteName: r.RouteName,
		Mode: r.Mode, StartedAt: r.StartedAt.UTC(), DurationS: s.DurationS, ElapsedS: s.ElapsedS, DistanceM: s.DistanceM,
		AscentM: s.AscentM, AvgPowerW: s.AvgPowerW, MaxPowerW: s.MaxPowerW, NPW: s.NPW, KJ: s.KJ,
		AvgCadenceRpm: s.AvgCadenceRpm, AvgHRBpm: s.AvgHRBpm, MaxHRBpm: s.MaxHRBpm, AvgSpeedKmh: s.AvgSpeedKmh,
		MaxSpeedKmh: s.MaxSpeedKmh, SampleCount: r.SampleCount,
		Strava: StravaStatusOut{Status: r.Strava.Status, UploadID: r.Strava.UploadID, ActivityID: r.Strava.ActivityID,
			Error: r.Strava.Error}}
	if o.Strava.Status == "" {
		o.Strava.Status = "none"
	}
	if r.Strava.ActivityID != 0 {
		o.Strava.ActivityURL = "https://www.strava.com/activities/" + strconv.FormatInt(r.Strava.ActivityID, 10)
	}
	if r.EndedAt != nil {
		t := r.EndedAt.UTC()
		o.EndedAt = &t
	}
	return o
}

// Series holds a ride's samples as parallel arrays, thinned to the requested number of points.
type Series struct {
	ElapsedS   []float64 `json:"elapsed_s" doc:"Riding time of each point in seconds."`
	DistanceM  []float64 `json:"distance_m" doc:"Distance of each point in metres."`
	PowerW     []float64 `json:"power_w" doc:"Power of each point in watts (the mean of its bucket)."`
	CadenceRpm []float64 `json:"cadence_rpm" doc:"Cadence of each point in revolutions per minute."`
	SpeedKmh   []float64 `json:"speed_kmh" doc:"Speed of each point in kilometres per hour."`
	GradePct   []float64 `json:"grade_pct" doc:"Road grade of each point in percent; 0 on a free ride."`
	Level      []int     `json:"level" doc:"Resistance level of each point, 1 to 32."`
	HRBpm      []int     `json:"hr_bpm" doc:"Heart rate of each point in beats per minute; 0 where none was read."`
	Lat        []float64 `json:"lat,omitempty" doc:"Latitude of each point in degrees. Present only when every point has a position."`
	Lon        []float64 `json:"lon,omitempty" doc:"Longitude of each point in degrees. Present only when every point has a position."`
	EleM       []float64 `json:"ele_m,omitempty" doc:"Elevation of each point in metres. Present only when every point has one."`
}

// finite maps NaN and infinities to 0 so a series never fails to encode.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// buildSeries thins ss to at most points and lays it out as arrays. A
// position array is left out unless every point carries that value, so the
// JSON never holds NaN (raw and thinned samples alike).
func buildSeries(ss []store.Sample, points int) Series {
	ds := history.Downsample(ss, points)
	n := len(ds)
	s := Series{ElapsedS: make([]float64, n), DistanceM: make([]float64, n), PowerW: make([]float64, n),
		CadenceRpm: make([]float64, n), SpeedKmh: make([]float64, n), GradePct: make([]float64, n),
		Level: make([]int, n), HRBpm: make([]int, n)}
	pos, ele := n > 0, n > 0
	for i, x := range ds {
		s.ElapsedS[i], s.DistanceM[i], s.PowerW[i] = finite(x.ElapsedS), finite(x.DistanceM), finite(x.PowerW)
		s.CadenceRpm[i], s.SpeedKmh[i], s.GradePct[i] = finite(x.CadenceRpm), finite(x.SpeedKmh), finite(x.GradePct)
		s.Level[i], s.HRBpm[i] = x.Level, x.HRBpm
		if math.IsNaN(x.Lat) || math.IsNaN(x.Lon) || math.IsInf(x.Lat, 0) || math.IsInf(x.Lon, 0) {
			pos = false
		}
		if math.IsNaN(x.EleM) || math.IsInf(x.EleM, 0) {
			ele = false
		}
	}
	if pos {
		s.Lat, s.Lon = make([]float64, n), make([]float64, n)
	}
	if ele {
		s.EleM = make([]float64, n)
	}
	for i, x := range ds {
		if pos {
			s.Lat[i], s.Lon[i] = x.Lat, x.Lon
		}
		if ele {
			s.EleM[i] = x.EleM
		}
	}
	return s
}

// RideDetail is a ride with its thinned sample series.
type RideDetail struct {
	RideOut
	Series Series `json:"series" doc:"The ride's samples, thinned to at most the requested number of points."`
}

// RideList is one page of rides.
type RideList struct {
	Rides      []RideOut `json:"rides" doc:"Finished rides, newest first."`
	NextBefore int64     `json:"next_before" doc:"Pass as before to get the next page; 0 when this is the last page."`
}

type listRidesIn struct {
	RiderID int64 `query:"rider_id" minimum:"0" doc:"Only this rider's rides. A rider of another user is a 404 unless you are an admin. Default: every ride of your own riders."`
	Before  int64 `query:"before" minimum:"0" doc:"Only rides with an ID below this one; pass the previous page's next_before. 0 starts at the newest."`
	Limit   int   `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"Page size, 1 to 100."`
}

type rideListBody struct{ Body RideList }

type rideIDIn struct {
	ID int64 `path:"id" doc:"Ride ID."`
}

type rideDetailIn struct {
	ID     int64 `path:"id" doc:"Ride ID."`
	Points int   `query:"points" minimum:"50" maximum:"3600" default:"600" doc:"Most points in the series, 50 to 3600. A longer ride is thinned by averaging."`
}

type rideDetailBody struct{ Body RideDetail }

type rideFitOut struct {
	ContentType string `header:"Content-Type"`
	Disposition string `header:"Content-Disposition"`
	Body        []byte
}

func errNoRide() error { return apiErr(http.StatusNotFound, "not_found", "no such ride") }

// ownedRide loads a finished ride the caller owns or, as an admin, may see.
// Anyone else gets 404; a ride still recording is 409 ride_in_progress.
func (d Deps) ownedRide(ctx context.Context, id int64) (store.Ride, error) {
	ident, _ := auth.FromContext(ctx)
	r, err := d.Store.Ride(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Ride{}, errNoRide()
	}
	if err != nil {
		return store.Ride{}, err
	}
	if !ident.Has(auth.ScopeAdmin) && (r.UserID == 0 || r.UserID != ident.User.ID) {
		return store.Ride{}, errNoRide()
	}
	if r.EndedAt == nil {
		return store.Ride{}, apiErr(http.StatusConflict, "ride_in_progress", "the ride is still recording")
	}
	return r, nil
}

// binaryResponse documents the 200 response of method path as a binary body of mediaType.
func binaryResponse(api huma.API, path, mediaType, desc string) {
	item := api.OpenAPI().Paths[path]
	if item == nil || item.Get == nil || item.Get.Responses["200"] == nil {
		return
	}
	resp := item.Get.Responses["200"]
	resp.Description = desc
	resp.Content = map[string]*huma.MediaType{mediaType: {Schema: &huma.Schema{Type: "string", Format: "binary"}}}
}

func (d Deps) registerRides(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/rides", ID: "listRides", Tag: "rides",
		Summary: "List rides",
		Description: "Returns finished rides of the caller's riders, newest first, without samples. Page by passing the " +
			"previous page's `next_before` as `before`. With `rider_id` only that rider's rides are listed; a rider of another " +
			"user returns 404 unless the caller is an admin.",
		Scope: auth.ScopeRead},
		func(ctx context.Context, in *listRidesIn) (*rideListBody, error) {
			ident, _ := auth.FromContext(ctx)
			q := store.RideQuery{Before: in.Before, Limit: in.Limit + 1}
			if in.RiderID != 0 {
				rd, err := d.Store.RiderByID(ctx, in.RiderID)
				if errors.Is(err, store.ErrNotFound) {
					return nil, errNoRider()
				}
				if err != nil {
					return nil, err
				}
				if rd.UserID != ident.User.ID && !ident.Has(auth.ScopeAdmin) {
					return nil, errNoRider()
				}
				q.RiderID = rd.ID
			} else {
				if ident.User.ID == 0 { // never fall through to an unfiltered, all-users query
					return nil, errors.New("rides: identity has no user")
				}
				q.UserID = ident.User.ID
			}
			rs, err := d.Store.ListRides(ctx, q)
			if err != nil {
				return nil, err
			}
			out := RideList{Rides: make([]RideOut, 0, min(len(rs), in.Limit))}
			if len(rs) > in.Limit {
				rs = rs[:in.Limit]
				out.NextBefore = rs[len(rs)-1].ID
			}
			for _, r := range rs {
				out.Rides = append(out.Rides, rideSummary(r))
			}
			return &rideListBody{Body: out}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/rides/{id}", ID: "getRideDetail", Tag: "rides",
		Summary: "Get a ride",
		Description: "Returns one finished ride with its samples thinned to at most `points` points (default 600) by " +
			"averaging. A ride of another user returns 404 unless the caller is an admin; a ride still recording is 409 `ride_in_progress`.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *rideDetailIn) (*rideDetailBody, error) {
			r, err := d.ownedRide(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			ss, err := d.Store.Samples(ctx, r.ID)
			if err != nil {
				return nil, err
			}
			return &rideDetailBody{Body: RideDetail{RideOut: rideSummary(r), Series: buildSeries(ss, in.Points)}}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/rides/{id}", ID: "deleteRideRecord", Tag: "rides",
		Summary: "Delete a ride",
		Description: "Deletes a finished ride and its samples. A Strava activity already created from it is not touched. " +
			"A ride of another user returns 404 unless the caller is an admin; a ride still recording is 409 `ride_in_progress`.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *rideIDIn) (*noContent, error) {
			r, err := d.ownedRide(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if err := d.Store.DeleteRide(ctx, r.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			return &noContent{}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/rides/{id}/fit", ID: "downloadRideFit", Tag: "rides",
		Summary: "Download a ride as FIT",
		Description: "Returns the ride as a FIT activity file (`application/vnd.ant.fit`) with a `Content-Disposition: attachment` " +
			"header. It does not depend on Strava. A ride of another user returns 404 unless the caller is an admin; a ride " +
			"still recording is 409 `ride_in_progress`.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *rideIDIn) (*rideFitOut, error) {
			r, err := d.ownedRide(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			ss, err := d.Store.Samples(ctx, r.ID)
			if err != nil {
				return nil, err
			}
			var buf bytes.Buffer
			if err := fitexport.Encode(&buf, r, ss); errors.Is(err, fitexport.ErrNoSamples) {
				return nil, apiErr(http.StatusNotFound, "not_found", "the ride has no samples")
			} else if err != nil {
				return nil, err
			}
			return &rideFitOut{ContentType: fitMediaType,
				Disposition: `attachment; filename="` + fitexport.Filename(r) + `"`, Body: buf.Bytes()}, nil
		})
	binaryResponse(api, "/api/rides/{id}/fit", fitMediaType, "The ride as a FIT activity file.")
}
