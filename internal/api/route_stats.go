package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/stats"
	"github.com/metril/wattson/internal/store"
)

type routeStatsIn struct {
	ID      int64 `path:"id" doc:"Route ID."`
	RiderID int64 `query:"rider_id" minimum:"0" doc:"Only this rider's rides. A rider of another user is a 404 unless you are an admin. Default: every ride of your own riders."`
}

// AttemptOut is one finished ride on a route.
type AttemptOut struct {
	RideID      int64     `json:"ride_id" doc:"Ride ID."`
	StartedAt   time.Time `json:"started_at" doc:"Start time, UTC."`
	ElapsedS    float64   `json:"elapsed_s" doc:"Ride time in seconds."`
	AvgSpeedKmh float64   `json:"avg_speed_kmh" doc:"Average speed in kilometres per hour."`
	NPW         float64   `json:"np_w" doc:"Normalized power in watts."`
	AvgPowerW   float64   `json:"avg_power_w" doc:"Average power in watts."`
	AvgHRBpm    int       `json:"avg_hr_bpm" doc:"Average heart rate in beats per minute; 0 when none was recorded."`
	Completed   bool      `json:"completed" doc:"True when the ride covered at least 98 percent of the route."`
	IsPR        bool      `json:"is_pr" doc:"True for the current personal record, the fastest completed attempt."`
}

// RouteStatsOut summarises a rider's attempts on a route.
type RouteStatsOut struct {
	Attempts     int          `json:"attempts" doc:"Number of finished rides on the route."`
	Completed    int          `json:"completed" doc:"Number of those that covered at least 98 percent of the route."`
	BestS        *float64     `json:"best_s" nullable:"true" doc:"Fastest completed time in seconds; null without a completed attempt."`
	AvgS         *float64     `json:"avg_s" nullable:"true" doc:"Average completed time in seconds."`
	LastS        *float64     `json:"last_s" nullable:"true" doc:"Time of the latest completed attempt in seconds."`
	BestSpeedKmh *float64     `json:"best_speed_kmh" nullable:"true" doc:"Highest average speed of a completed attempt in kilometres per hour."`
	PRRideID     int64        `json:"pr_ride_id" doc:"ID of the PR ride; 0 when there is none."`
	PRDate       *time.Time   `json:"pr_date" nullable:"true" doc:"Start time of the PR ride, UTC."`
	BestNPW      *float64     `json:"best_np_w" nullable:"true" doc:"Highest normalized power of a completed attempt in watts."`
	AvgPowerW    *float64     `json:"avg_power_w" nullable:"true" doc:"Average power over completed attempts in watts."`
	AvgHRBpm     *float64     `json:"avg_hr_bpm" nullable:"true" doc:"Average heart rate over completed attempts that recorded one."`
	List         []AttemptOut `json:"list" doc:"Every finished attempt, oldest first."`
}

type routeStatsBody struct {
	Body RouteStatsOut
}

// GhostOut is the PR ride's track for a live ghost.
type GhostOut struct {
	TimeS  float64      `json:"time_s" doc:"The PR time in seconds."`
	Points [][2]float64 `json:"points" doc:"[distance_m, elapsed_s] pairs at least 10 metres apart, in ride order."`
}

type ghostBody struct {
	Body GhostOut
}

// riderScope resolves the optional rider_id to (riderID, userID) for a query,
// applying the same ownership rule as listing rides.
func (d Deps) riderScope(ctx context.Context, riderID int64) (rider, user int64, err error) {
	ident, _ := auth.FromContext(ctx)
	if riderID != 0 {
		rd, err := d.Store.RiderByID(ctx, riderID)
		if errors.Is(err, store.ErrNotFound) {
			return 0, 0, errNoRider()
		}
		if err != nil {
			return 0, 0, err
		}
		if rd.UserID != ident.User.ID && !ident.Has(auth.ScopeAdmin) {
			return 0, 0, errNoRider()
		}
		return rd.ID, 0, nil
	}
	if ident.User.ID == 0 {
		return 0, 0, errors.New("route stats: identity has no user")
	}
	return 0, ident.User.ID, nil
}

func (d Deps) registerRouteStats(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/routes/{id}/stats", ID: "getRouteStats", Tag: "routes",
		Summary: "Get a route's attempt statistics",
		Description: "Returns the caller's finished rides on the route with best, average and last time, the personal " +
			"record and an attempt list. A completed attempt covers at least 98 percent of the route. With `rider_id` only " +
			"that rider's rides count; a rider of another user returns 404 unless the caller is an admin.",
		Scope: auth.ScopeRead},
		func(ctx context.Context, in *routeStatsIn) (*routeStatsBody, error) {
			rider, user, err := d.riderScope(ctx, in.RiderID)
			if err != nil {
				return nil, err
			}
			if _, err := d.Store.RouteDistance(ctx, in.ID); errors.Is(err, store.ErrNotFound) {
				return nil, errNoRoute()
			} else if err != nil {
				return nil, err
			}
			as, err := d.Store.RouteAttempts(ctx, in.ID, rider, user)
			if err != nil {
				return nil, err
			}
			s := stats.SummarizeRoute(as)
			out := RouteStatsOut{Attempts: s.Attempts, Completed: s.Completed, BestS: s.BestS, AvgS: s.AvgS,
				LastS: s.LastS, BestSpeedKmh: s.BestSpeedKmh, PRRideID: s.PRRideID, PRDate: s.PRDate,
				BestNPW: s.BestNPW, AvgPowerW: s.AvgPowerW, AvgHRBpm: s.AvgHRBpm, List: make([]AttemptOut, len(as))}
			for i, a := range as {
				out.List[i] = AttemptOut{RideID: a.RideID, StartedAt: a.StartedAt, ElapsedS: a.ElapsedS,
					AvgSpeedKmh: a.AvgSpeedKmh, NPW: a.NPW, AvgPowerW: a.AvgPowerW, AvgHRBpm: a.AvgHRBpm,
					Completed: a.Completed, IsPR: s.IsPR[i]}
			}
			return &routeStatsBody{Body: out}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/routes/{id}/ghost", ID: "getRouteGhost", Tag: "routes",
		Summary: "Get the PR ghost of a route",
		Description: "Returns the track of the caller's fastest completed ride on the route as `[distance_m, elapsed_s]` " +
			"points at least 10 metres apart. Errors: 404 `no_pr` when there is no completed ride. With `rider_id` only " +
			"that rider's rides count.",
		Scope: auth.ScopeRead},
		func(ctx context.Context, in *routeStatsIn) (*ghostBody, error) {
			rider, user, err := d.riderScope(ctx, in.RiderID)
			if err != nil {
				return nil, err
			}
			dist, el, err := d.Store.RoutePRTrack(ctx, in.ID, rider, user)
			if err != nil {
				return nil, err
			}
			if len(dist) == 0 {
				return nil, apiErr(http.StatusNotFound, "no_pr", "no completed ride on this route yet")
			}
			return &ghostBody{Body: GhostOut{TimeS: el[len(el)-1], Points: stats.GhostTrack(dist, el)}}, nil
		})
}
