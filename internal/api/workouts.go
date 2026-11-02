package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/workout"
)

// BlockIO is one step of a workout. Interval workouts set level or
// level_offset; ERG workouts set power_w or ftp_pct.
type BlockIO struct {
	DurationS   int    `json:"duration_s" minimum:"5" maximum:"7200" doc:"Block length in seconds, 5 to 7200."`
	Level       *int   `json:"level,omitempty" minimum:"1" maximum:"32" doc:"Interval only: absolute resistance level, 1 to 32 (clamped to the rider's min and max level when ridden)."`
	LevelOffset *int   `json:"level_offset,omitempty" minimum:"-31" maximum:"31" doc:"Interval only: resistance level relative to the rider's flat-road offset level, -31 to 31 (clamped to the rider's min and max level when ridden)."`
	PowerW      *int   `json:"power_w,omitempty" minimum:"50" maximum:"1000" doc:"ERG only: target power in watts, 50 to 1000."`
	FTPPct      *int   `json:"ftp_pct,omitempty" minimum:"20" maximum:"200" doc:"ERG only: target power as a percent of the rider's FTP, 20 to 200."`
	Cadence     string `json:"cadence,omitempty" maxLength:"32" doc:"Cadence hint shown to the rider, for example 90-100 (rpm); at most 32 characters. Not enforced."`
	Label       string `json:"label,omitempty" maxLength:"40" doc:"Short block label, at most 40 characters."`
}

// WorkoutIn is the body of create and replace.
type WorkoutIn struct {
	Name   string    `json:"name" minLength:"1" maxLength:"100" doc:"Display name, 1 to 100 characters."`
	Type   string    `json:"type" enum:"interval,erg" doc:"interval sets a resistance level per block; erg holds a target power per block."`
	Blocks []BlockIO `json:"blocks" minItems:"1" maxItems:"200" doc:"Blocks ridden in order, 1 to 200; the total must not exceed 21600 s (6 h)."`
}

// WorkoutOut is a stored workout.
type WorkoutOut struct {
	ID        int64     `json:"id" doc:"Workout ID."`
	Name      string    `json:"name" doc:"Display name."`
	Type      string    `json:"type" enum:"interval,erg" doc:"interval sets a resistance level per block; erg holds a target power per block."`
	OwnerID   int64     `json:"owner_id" doc:"ID of the user who created the workout; 0 when that user has since been deleted."`
	DurationS int       `json:"duration_s" doc:"Total length of all blocks in seconds."`
	Blocks    []BlockIO `json:"blocks" doc:"Blocks ridden in order."`
	CreatedAt time.Time `json:"created_at" doc:"Creation time, UTC."`
	UpdatedAt time.Time `json:"updated_at" doc:"Last change time, UTC."`
}

type workoutIDIn struct {
	ID int64 `path:"id" doc:"Workout ID."`
}

type workoutsOut struct{ Body []WorkoutOut }
type workoutBody struct{ Body WorkoutOut }
type createWorkoutIn struct{ Body WorkoutIn }
type putWorkoutIn struct {
	ID   int64 `path:"id" doc:"Workout ID."`
	Body WorkoutIn
}

func errNoWorkout() error { return apiErr(http.StatusNotFound, "not_found", "no such workout") }

func workoutOut(w store.Workout) (WorkoutOut, error) {
	out := WorkoutOut{ID: w.ID, Name: w.Name, Type: w.Type, OwnerID: w.UserID, DurationS: w.DurationS,
		Blocks: []BlockIO{}, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
	if err := json.Unmarshal(w.Blocks, &out.Blocks); err != nil {
		return WorkoutOut{}, err
	}
	return out, nil
}

func toBlocks(in []BlockIO) []workout.Block {
	bs := make([]workout.Block, len(in))
	for i, b := range in {
		bs[i] = workout.Block(b)
	}
	return bs
}

// workoutRecord validates body and builds the unsaved workout (ID, owner and
// times unset).
func workoutRecord(in WorkoutIn) (store.Workout, error) {
	name := cleanName(in.Name)
	if name == "" {
		return store.Workout{}, apiErr(http.StatusBadRequest, "invalid_request", "name: must not be blank")
	}
	bs := toBlocks(in.Blocks)
	if err := workout.Validate(workout.Type(in.Type), bs); err != nil {
		var ve *workout.ValidationError
		if errors.As(err, &ve) {
			return store.Workout{}, apiErr(http.StatusBadRequest, "invalid_request", ve.Error())
		}
		return store.Workout{}, err
	}
	raw, err := json.Marshal(bs)
	if err != nil {
		return store.Workout{}, err
	}
	return store.Workout{Name: name, Type: in.Type, Blocks: raw, DurationS: workout.DurationS(bs)}, nil
}

// authorizeWorkout loads workout id and refuses callers who neither own it nor are admins.
func (d Deps) authorizeWorkout(ctx context.Context, id int64) (store.Workout, error) {
	ident, _ := auth.FromContext(ctx)
	w, err := d.Store.Workout(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Workout{}, errNoWorkout()
	}
	if err != nil {
		return store.Workout{}, err
	}
	if !ident.Has(auth.ScopeAdmin) && (w.UserID == 0 || w.UserID != ident.User.ID) {
		return store.Workout{}, apiErr(http.StatusForbidden, "not_owner", "only the owner or an admin can change this workout")
	}
	return w, nil
}

func (d Deps) registerWorkouts(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/workouts", ID: "listWorkouts", Tag: "workouts",
		Summary:     "List workouts",
		Description: "Returns every workout with its blocks, newest first. Workouts are shared: every signed-in user sees all of them.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, _ *struct{}) (*workoutsOut, error) {
			ws, err := d.Store.ListWorkouts(ctx)
			if err != nil {
				return nil, err
			}
			out := &workoutsOut{Body: make([]WorkoutOut, 0, len(ws))}
			for _, w := range ws {
				o, err := workoutOut(w)
				if err != nil {
					return nil, err
				}
				out.Body = append(out.Body, o)
			}
			return out, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/workouts/{id}", ID: "getWorkout", Tag: "workouts",
		Summary:     "Get a workout",
		Description: "Returns a workout with its blocks. Any signed-in user may read any workout.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, in *workoutIDIn) (*workoutBody, error) {
			w, err := d.Store.Workout(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoWorkout()
			}
			if err != nil {
				return nil, err
			}
			o, err := workoutOut(w)
			if err != nil {
				return nil, err
			}
			return &workoutBody{Body: o}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/workouts", ID: "createWorkout", Tag: "workouts",
		Summary: "Create a workout",
		Description: "Creates a workout owned by the caller. Blocks are validated: 1 to 200 blocks of 5 to 7200 s, at most 21600 s in total; " +
			"interval blocks set exactly one of `level` or `level_offset`, ERG blocks exactly one of `power_w` or `ftp_pct`. " +
			"A violation is a 400 `invalid_request` whose message names the field, for example `blocks[2].power_w: exactly one of power_w or ftp_pct required for erg`.",
		Scope: auth.ScopeControl, Status: http.StatusCreated},
		func(ctx context.Context, in *createWorkoutIn) (*workoutBody, error) {
			ident, _ := auth.FromContext(ctx)
			w, err := workoutRecord(in.Body)
			if err != nil {
				return nil, err
			}
			w.UserID = ident.User.ID
			w, err = d.Store.CreateWorkout(ctx, w)
			if err != nil {
				return nil, err
			}
			o, err := workoutOut(w)
			if err != nil {
				return nil, err
			}
			return &workoutBody{Body: o}, nil
		})

	register(api, route{Method: http.MethodPut, Path: "/api/workouts/{id}", ID: "updateWorkout", Tag: "workouts",
		Summary: "Replace a workout",
		Description: "Replaces the workout's name, type and blocks. Only the workout's owner or an admin may do this (403 `not_owner` otherwise). " +
			"Blocks are resolved when a ride starts, so an edit applies to the next ride only. Validation errors are as for create.",
		Scope: auth.ScopeControl},
		func(ctx context.Context, in *putWorkoutIn) (*workoutBody, error) {
			if _, err := d.authorizeWorkout(ctx, in.ID); err != nil {
				return nil, err
			}
			w, err := workoutRecord(in.Body)
			if err != nil {
				return nil, err
			}
			w.ID = in.ID
			w, err = d.Store.UpdateWorkout(ctx, w)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoWorkout()
			}
			if err != nil {
				return nil, err
			}
			o, err := workoutOut(w)
			if err != nil {
				return nil, err
			}
			return &workoutBody{Body: o}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/workouts/{id}", ID: "deleteWorkout", Tag: "workouts",
		Summary: "Delete a workout",
		Description: "Deletes a workout. Only the workout's owner or an admin may do this (403 `not_owner` otherwise). " +
			"A workout the connected ride is using cannot be deleted (409 `workout_in_use`); stop the ride first.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *workoutIDIn) (*noContent, error) {
			if _, err := d.authorizeWorkout(ctx, in.ID); err != nil {
				return nil, err
			}
			if d.WorkoutInUse != nil && d.WorkoutInUse(in.ID) {
				return nil, apiErr(http.StatusConflict, "workout_in_use", "the workout is in use by the current ride; stop the ride first")
			}
			if err := d.Store.DeleteWorkout(ctx, in.ID); errors.Is(err, store.ErrNotFound) {
				return nil, errNoWorkout()
			} else if err != nil {
				return nil, err
			}
			return &noContent{}, nil
		})
}
