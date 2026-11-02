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
	case errors.Is(err, engine.ErrUnknownCommand):
		return http.StatusBadRequest, "invalid_request", "unknown command", true
	}
	return 0, "", "", false
}

func (d Deps) registerRide(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/ride", ID: "getRide", Tag: "ride",
		Summary:     "Get the current ride",
		Description: "Returns the latest ride snapshot: state, speed, power, cadence, distance and resistance level. In state `idle` when no bike is connected.",
		Scope:       auth.ScopeRead},
		func(_ context.Context, _ *noIn) (*rideOut, error) {
			return &rideOut{Body: d.Bike.Snapshot()}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/ride/command", ID: "rideCommand", Tag: "ride",
		Summary: "Send a ride command",
		Description: "Starts, pauses, resumes or stops the ride, or changes the resistance level. Any user with control may drive the single bike. " +
			"Returns 409 `no_bike` when no bike is connected and 409 `bad_state` when the command does not fit the ride state (for example resume while not paused).",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *commandIn) (*noContent, error) {
			kind, known := rideKinds[in.Body.Command]
			if !known {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "command: unknown command")
			}
			if err := d.Bike.Do(ctx, engine.Command{Kind: kind, Value: in.Body.Value}); err != nil {
				if status, code, msg, ok := commandFailure(err); ok {
					return nil, apiErr(status, code, msg)
				}
				return nil, err
			}
			return &noContent{}, nil
		})
}
