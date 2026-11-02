package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/ridectl"
)

type rideOut struct {
	Body engine.Tick
}

type commandIn struct {
	Body struct {
		Command string `json:"command" enum:"start,pause,resume,stop,set_level,level_delta" doc:"Ride command: start, pause, resume, stop, set_level (to value) or level_delta (by value)."`
		Value   int    `json:"value,omitempty" doc:"Resistance level for set_level, or the change in levels for level_delta; ignored by other commands."`
		RouteID int64  `json:"route_id,omitempty" doc:"Route to ride; only with start. The ride follows the route's grade using the rider bound to the bike."`
	}
}

// rideKinds maps a wire command name onto the engine command.
var rideKinds = map[string]engine.CmdKind{
	"start": engine.CmdStart, "pause": engine.CmdPause, "resume": engine.CmdResume, "stop": engine.CmdStop,
	"set_level": engine.CmdSetLevel, "level_delta": engine.CmdLevelDelta,
}

// commandFailure translates a Manager.Do error into an HTTP status and code
// shared by the REST and WebSocket command paths; ok is false for an error
// that is none of the expected ones.
func commandFailure(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, ridectl.ErrNoBike), errors.Is(err, engine.ErrStopped):
		return http.StatusConflict, "no_bike", "no bike is connected or the ride has just ended", true
	case errors.Is(err, engine.ErrBadState):
		return http.StatusConflict, "bad_state", "that command is not valid in the current ride state", true
	case errors.Is(err, ridectl.ErrRouteNotFound):
		return http.StatusNotFound, "route_not_found", "no such route", true
	case errors.Is(err, engine.ErrUnknownCommand):
		return http.StatusBadRequest, "invalid_request", "unknown command", true
	}
	return 0, "", "", false
}

// errRouteIDOnlyStart is the message for a route_id sent with any command but start.
const errRouteIDOnlyStart = "route_id is only valid with start"

// runCommand sends one ride command; start with a route id starts a route ride.
func runCommand(ctx context.Context, m *ridectl.Manager, kind engine.CmdKind, value int, routeID int64) error {
	if kind == engine.CmdStart && routeID != 0 {
		return m.StartRoute(ctx, routeID)
	}
	return m.Do(ctx, engine.Command{Kind: kind, Value: value})
}

func (d Deps) registerRide(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/ride", ID: "getRide", Tag: "ride",
		Summary:     "Get the current ride",
		Description: "Returns the latest ride snapshot: state, speed, power, cadence, distance and resistance level. In state `idle` when no bike is connected.",
		Scope:       auth.ScopeRead, Errors: []int{http.StatusServiceUnavailable}},
		func(_ context.Context, _ *noIn) (*rideOut, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			return &rideOut{Body: m.Snapshot()}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/ride/command", ID: "rideCommand", Tag: "ride",
		Summary: "Send a ride command",
		Description: "Starts, pauses, resumes or stops the ride, or changes the resistance level. Any user with control may drive the single bike. " +
			"`start` with `route_id` rides that route with the rider bound to the bike; the ride ends by itself at the route's end. `route_id` with any other command is a 400 `invalid_request`. " +
			"Returns 404 `route_not_found` for an unknown route, 409 `no_bike` when no bike is connected and 409 `bad_state` when the command does not fit the ride state (for example resume while not paused).",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *commandIn) (*noContent, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			kind, known := rideKinds[in.Body.Command]
			if !known {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "command: unknown command")
			}
			if in.Body.RouteID != 0 && kind != engine.CmdStart {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "route_id: "+errRouteIDOnlyStart)
			}
			if err := runCommand(ctx, m, kind, in.Body.Value, in.Body.RouteID); err != nil {
				if status, code, msg, ok := commandFailure(err); ok {
					return nil, apiErr(status, code, msg)
				}
				return nil, err
			}
			return &noContent{}, nil
		})
}
