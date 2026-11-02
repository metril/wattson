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
		Command      string `json:"command" enum:"start,pause,resume,stop,set_level,level_delta,set_target_power" doc:"Ride command: start, pause, resume, stop, set_level (to value), level_delta (by value) or set_target_power (free ERG target, value in watts)."`
		Value        int    `json:"value,omitempty" doc:"Resistance level for set_level, the change in levels for level_delta (in ERG, steps of 10 W of target), or the target in watts (50 to 1000) for set_target_power; ignored by other commands."`
		RouteID      int64  `json:"route_id,omitempty" doc:"Route to ride; only with start. The ride follows the route's grade using the rider bound to the bike."`
		WorkoutID    int64  `json:"workout_id,omitempty" doc:"Workout to ride; only with start. Its blocks are resolved for the rider bound to the bike when the ride starts."`
		TargetPowerW int    `json:"target_power_w,omitempty" doc:"Free ERG target, W (50-1000); only with start. Hold this power until stopped."`
	}
}

// rideKinds maps a wire command name onto the engine command.
var rideKinds = map[string]engine.CmdKind{
	"start": engine.CmdStart, "pause": engine.CmdPause, "resume": engine.CmdResume, "stop": engine.CmdStop,
	"set_level": engine.CmdSetLevel, "level_delta": engine.CmdLevelDelta, "set_target_power": engine.CmdSetTargetPower,
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
	case errors.Is(err, ridectl.ErrWorkoutNotFound):
		return http.StatusNotFound, "workout_not_found", "no such workout", true
	case errors.Is(err, engine.ErrBadValue):
		return http.StatusBadRequest, "invalid_value", "value is out of range (a target power must be 50 to 1000 W)", true
	case errors.Is(err, engine.ErrUnknownCommand):
		return http.StatusBadRequest, "invalid_request", "unknown command", true
	}
	return 0, "", "", false
}

// startArgs are the fields that only accompany start; at most one may be set.
type startArgs struct {
	RouteID      int64
	WorkoutID    int64
	TargetPowerW int
}

// check returns the invalid_request message for a misuse of the start fields,
// or "" when they are fine for command kind.
func (a startArgs) check(kind engine.CmdKind) string {
	set := 0
	only := ""
	for _, f := range []struct {
		name string
		on   bool
	}{{"route_id", a.RouteID != 0}, {"workout_id", a.WorkoutID != 0}, {"target_power_w", a.TargetPowerW != 0}} {
		if f.on {
			set++
			if only == "" {
				only = f.name
			}
		}
	}
	switch {
	case set > 0 && kind != engine.CmdStart:
		return only + " is only valid with start"
	case set > 1:
		return "choose one of route_id, workout_id, target_power_w"
	}
	return ""
}

// runCommand sends one ride command; start with a route, workout or target
// power starts that kind of ride.
func runCommand(ctx context.Context, m *ridectl.Manager, kind engine.CmdKind, value int, a startArgs) error {
	if kind == engine.CmdStart {
		switch {
		case a.RouteID != 0:
			return m.StartRoute(ctx, a.RouteID)
		case a.WorkoutID != 0:
			return m.StartWorkout(ctx, a.WorkoutID)
		case a.TargetPowerW != 0:
			return m.StartErg(ctx, a.TargetPowerW)
		}
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
			"`start` with `route_id` rides that route with the rider bound to the bike; the ride ends by itself at the route's end. " +
			"`start` with `workout_id` rides that interval or ERG workout, resolved for the bound rider when the ride starts (editing it later does not change this ride); the ride ends by itself after the last block. " +
			"`start` with `target_power_w` rides free ERG at that power until stopped; `set_target_power` (value in watts) changes it. " +
			"`start` takes at most one of `route_id`, `workout_id` and `target_power_w`, and these fields with any other command are a 400 `invalid_request`. " +
			"Returns 404 `route_not_found` or `workout_not_found` for an unknown route or workout, 400 `invalid_value` for a target power outside 50 to 1000 W, 409 `no_bike` when no bike is connected and 409 `bad_state` when the command does not fit the ride state (for example resume while not paused).",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusServiceUnavailable}},
		func(ctx context.Context, in *commandIn) (*noContent, error) {
			m, err := d.bikeMgr()
			if err != nil {
				return nil, err
			}
			kind, known := rideKinds[in.Body.Command]
			if !known {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "command: unknown command")
			}
			args := startArgs{RouteID: in.Body.RouteID, WorkoutID: in.Body.WorkoutID, TargetPowerW: in.Body.TargetPowerW}
			if msg := args.check(kind); msg != "" {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", msg)
			}
			if err := runCommand(ctx, m, kind, in.Body.Value, args); err != nil {
				if status, code, msg, ok := commandFailure(err); ok {
					return nil, apiErr(status, code, msg)
				}
				return nil, err
			}
			return &noContent{}, nil
		})
}
