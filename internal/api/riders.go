package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

// RiderOut is a riding profile.
type RiderOut struct {
	ID              int64     `json:"id" doc:"Rider ID."`
	UserID          int64     `json:"user_id" doc:"ID of the user who owns this rider."`
	Name            string    `json:"name" doc:"Display name of the rider."`
	WeightKg        float64   `json:"weight_kg" doc:"Rider mass in kilograms."`
	BikeKg          float64   `json:"bike_kg" doc:"Virtual bike mass in kilograms."`
	FTPW            int       `json:"ftp_w" doc:"Functional threshold power in watts."`
	MinLevel        int       `json:"min_level" doc:"Lowest resistance level used in rides, 1 to 32. The bike returns to this level on release."`
	MaxLevel        int       `json:"max_level" doc:"Highest resistance level used in rides, 1 to 32."`
	Gain            float64   `json:"gain" doc:"Resistance levels added per 1 percent of road grade."`
	OffsetLevel     float64   `json:"offset_level" doc:"Resistance level at 0 percent grade; also the start level of a free ride."`
	DownhillScale   float64   `json:"downhill_scale" doc:"Factor applied to negative grades, 0 to 1. Lower values give less relief on descents."`
	CdA             float64   `json:"cda" doc:"Drag area (drag coefficient times frontal area) in square metres."`
	Crr             float64   `json:"crr" doc:"Rolling resistance coefficient, dimensionless."`
	PowerMultiplier float64   `json:"power_multiplier" doc:"Factor applied to the bike's measured power, dimensionless."`
	CreatedAt       time.Time `json:"created_at" doc:"Creation time, UTC."`
	UpdatedAt       time.Time `json:"updated_at" doc:"Last change time, UTC."`
}

func riderOut(r store.Rider) RiderOut {
	return RiderOut{ID: r.ID, UserID: r.UserID, Name: r.Name, WeightKg: r.WeightKg, BikeKg: r.BikeKg,
		FTPW: r.FTPW, MinLevel: r.MinLevel, MaxLevel: r.MaxLevel, Gain: r.Gain, OffsetLevel: r.OffsetLevel,
		DownhillScale: r.DownhillScale, CdA: r.CdA, Crr: r.Crr, PowerMultiplier: r.PowerMultiplier,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// RiderParams are the optional profile fields shared by create and update.
type RiderParams struct {
	WeightKg        *float64 `json:"weight_kg,omitempty" minimum:"30" maximum:"200" doc:"Rider mass in kilograms, 30 to 200. Default 75."`
	BikeKg          *float64 `json:"bike_kg,omitempty" minimum:"0" maximum:"30" doc:"Virtual bike mass in kilograms, 0 to 30. Default 8."`
	FTPW            *int     `json:"ftp_w,omitempty" minimum:"50" maximum:"600" doc:"Functional threshold power in watts, 50 to 600. Default 200."`
	MinLevel        *int     `json:"min_level,omitempty" minimum:"1" maximum:"32" doc:"Lowest resistance level, 1 to 32 and at most max_level. Default 1."`
	MaxLevel        *int     `json:"max_level,omitempty" minimum:"1" maximum:"32" doc:"Highest resistance level, 1 to 32 and at least min_level. Default 16."`
	Gain            *float64 `json:"gain,omitempty" minimum:"0" maximum:"5" doc:"Resistance levels per 1 percent grade, 0 to 5. Default 1.0."`
	OffsetLevel     *float64 `json:"offset_level,omitempty" minimum:"1" maximum:"32" doc:"Resistance level at 0 percent grade and free-ride start level, 1 to 32, between min_level and max_level. Default 5."`
	DownhillScale   *float64 `json:"downhill_scale,omitempty" minimum:"0" maximum:"1" doc:"Factor on negative grades, 0 to 1. Default 0.5."`
	CdA             *float64 `json:"cda,omitempty" minimum:"0.15" maximum:"0.8" doc:"Drag area in square metres, 0.15 to 0.8. Default 0.32."`
	Crr             *float64 `json:"crr,omitempty" minimum:"0.001" maximum:"0.02" doc:"Rolling resistance coefficient, 0.001 to 0.02. Default 0.004."`
	PowerMultiplier *float64 `json:"power_multiplier,omitempty" minimum:"0.5" maximum:"2" doc:"Factor on measured power, 0.5 to 2.0. Default 1.0."`
}

// apply copies the fields present in p onto r.
func (p RiderParams) apply(r *store.Rider) {
	set(&r.WeightKg, p.WeightKg)
	set(&r.BikeKg, p.BikeKg)
	set(&r.FTPW, p.FTPW)
	set(&r.MinLevel, p.MinLevel)
	set(&r.MaxLevel, p.MaxLevel)
	set(&r.Gain, p.Gain)
	set(&r.OffsetLevel, p.OffsetLevel)
	set(&r.DownhillScale, p.DownhillScale)
	set(&r.CdA, p.CdA)
	set(&r.Crr, p.Crr)
	set(&r.PowerMultiplier, p.PowerMultiplier)
}

// checkRider validates the rules that span fields or need trimming.
func checkRider(r *store.Rider) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return apiErr(http.StatusBadRequest, "invalid_request", "name: must not be blank")
	}
	if r.MinLevel > r.MaxLevel {
		return apiErr(http.StatusBadRequest, "invalid_request", "min_level: must not exceed max_level")
	}
	if o := r.OffsetLevel; o < float64(r.MinLevel) || o > float64(r.MaxLevel) {
		return apiErr(http.StatusBadRequest, "invalid_request", "offset_level: must be between min_level and max_level")
	}
	return nil
}

type listRidersIn struct {
	All bool `query:"all" doc:"Admin only: list the riders of every user instead of only your own."`
}

type ridersOut struct {
	Body []RiderOut
}

type riderOutBody struct {
	Body RiderOut
}

type createRiderIn struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"64" doc:"Display name of the rider, 1 to 64 characters."`
		RiderParams
	}
}

type riderIDIn struct {
	ID int64 `path:"id" doc:"Rider ID."`
}

type patchRiderIn struct {
	ID   int64 `path:"id" doc:"Rider ID."`
	Body struct {
		Name *string `json:"name,omitempty" minLength:"1" maxLength:"64" doc:"Display name of the rider, 1 to 64 characters."`
		RiderParams
	}
}

func errNoRider() error { return apiErr(http.StatusNotFound, "not_found", "no such rider") }

func (d Deps) registerRiders(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/riders", ID: "listRiders", Tag: "riders",
		Summary: "List riders",
		Description: "Returns the caller's riders, ordered by ID. A user with no rider gets a default one (named after " +
			"their display name or username) created on the first call. An admin may pass `all=true` to list every " +
			"user's riders; other users get 403.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusForbidden}},
		func(ctx context.Context, in *listRidersIn) (*ridersOut, error) {
			id, _ := auth.FromContext(ctx)
			var rs []store.Rider
			var err error
			if in.All {
				if !id.Has(auth.ScopeAdmin) {
					return nil, apiErr(http.StatusForbidden, "forbidden", "missing required scope: admin")
				}
				rs, err = d.Store.ListAllRiders(ctx)
			} else {
				name := id.User.DisplayName
				if name == "" {
					name = id.User.Username
				}
				if err = d.Store.EnsureRider(ctx, store.DefaultRider(id.User.ID, name)); err != nil {
					return nil, err
				}
				rs, err = d.Store.ListRiders(ctx, id.User.ID)
			}
			if err != nil {
				return nil, err
			}
			out := &ridersOut{Body: make([]RiderOut, 0, len(rs))}
			for _, r := range rs {
				out.Body = append(out.Body, riderOut(r))
			}
			return out, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/riders", ID: "createRider", Tag: "riders",
		Summary:     "Create a rider",
		Description: "Creates a rider owned by the caller. Omitted fields take their documented defaults.",
		Scope:       auth.ScopeControl, Status: http.StatusCreated},
		func(ctx context.Context, in *createRiderIn) (*riderOutBody, error) {
			id, _ := auth.FromContext(ctx)
			r := store.DefaultRider(id.User.ID, in.Body.Name)
			in.Body.RiderParams.apply(&r)
			if err := checkRider(&r); err != nil {
				return nil, err
			}
			r, err := d.Store.CreateRider(ctx, r)
			if err != nil {
				return nil, err
			}
			return &riderOutBody{Body: riderOut(r)}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/riders/{id}", ID: "getRider", Tag: "riders",
		Summary:     "Get a rider",
		Description: "Returns one of the caller's riders. A rider owned by someone else returns 404.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, in *riderIDIn) (*riderOutBody, error) {
			id, _ := auth.FromContext(ctx)
			r, err := d.Store.Rider(ctx, id.User.ID, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRider()
			}
			if err != nil {
				return nil, err
			}
			return &riderOutBody{Body: riderOut(r)}, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/riders/{id}", ID: "updateRider", Tag: "riders",
		Summary: "Update a rider",
		Description: "Changes the fields present in the body of one of the caller's riders. The merged profile is " +
			"validated (`min_level` must not exceed `max_level`). A rider owned by someone else returns 404. Changing the rider " +
			"bound to the connected bike is allowed, but the connection keeps the profile it was made with: the change " +
			"applies at the next connect.",
		Scope: auth.ScopeControl},
		func(ctx context.Context, in *patchRiderIn) (*riderOutBody, error) {
			id, _ := auth.FromContext(ctx)
			r, err := d.Store.Rider(ctx, id.User.ID, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRider()
			}
			if err != nil {
				return nil, err
			}
			set(&r.Name, in.Body.Name)
			in.Body.RiderParams.apply(&r)
			if err := checkRider(&r); err != nil {
				return nil, err
			}
			r, err = d.Store.UpdateRider(ctx, r)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoRider()
			}
			if err != nil {
				return nil, err
			}
			return &riderOutBody{Body: riderOut(r)}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/riders/{id}", ID: "deleteRider", Tag: "riders",
		Summary: "Delete a rider",
		Description: "Deletes one of the caller's riders. The last remaining rider cannot be deleted (409 `last_rider`), " +
			"nor can the rider bound to the connected bike (409 `rider_in_use`; disconnect the bike first). " +
			"A rider owned by someone else returns 404.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *riderIDIn) (*noContent, error) {
			id, _ := auth.FromContext(ctx)
			if d.RiderInUse != nil && d.RiderInUse(in.ID) {
				// Only the owner learns the rider exists; anyone else gets 404.
				if _, err := d.Store.Rider(ctx, id.User.ID, in.ID); errors.Is(err, store.ErrNotFound) {
					return nil, errNoRider()
				} else if err != nil {
					return nil, err
				}
				return nil, apiErr(http.StatusConflict, "rider_in_use", "the rider is bound to the connected bike; disconnect it first")
			}
			err := d.Store.DeleteRider(ctx, id.User.ID, in.ID)
			switch {
			case errors.Is(err, store.ErrNotFound):
				return nil, errNoRider()
			case errors.Is(err, store.ErrLastRider):
				return nil, apiErr(http.StatusConflict, "last_rider", "the last rider cannot be deleted")
			case err != nil:
				return nil, err
			}
			return &noContent{}, nil
		})
}
