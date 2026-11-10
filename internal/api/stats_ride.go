package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
)

// RideCurvePoint is one point of a single ride's power curve.
type RideCurvePoint struct {
	DurS  int     `json:"dur_s" doc:"Duration in seconds."`
	Watts float64 `json:"watts" doc:"Best average power over this duration within the ride, in watts."`
}

// RideCurveOut is a single ride's power curve.
type RideCurveOut struct {
	Points []RideCurvePoint `json:"points" doc:"Shortest duration first; durations longer than the ride are absent. Empty for a ride not yet computed."`
}

func (d Deps) registerRideCurve(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/rides/{id}/power-curve", ID: "getRidePowerCurve", Tag: "rides",
		Summary: "Get a ride's power curve",
		Description: "Returns the best average power of one finished ride for each of about 45 durations from 1 second to 2 hours. " +
			"A ride of another user returns 404 unless the caller is an admin; a ride still recording is 409 `ride_in_progress`.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusNotFound, http.StatusConflict}},
		func(ctx context.Context, in *rideIDIn) (*struct{ Body RideCurveOut }, error) {
			r, err := d.ownedRide(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			pts, err := d.Store.RidePowerCurve(ctx, r.ID)
			if err != nil {
				return nil, err
			}
			out := RideCurveOut{Points: make([]RideCurvePoint, len(pts))}
			for i, p := range pts {
				out.Points[i] = RideCurvePoint{DurS: p.DurS, Watts: p.Watts}
			}
			return &struct{ Body RideCurveOut }{out}, nil
		})
}
