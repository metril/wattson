package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

// UserOut is a user account as returned by the API. Password hashes and
// OIDC identifiers never leave the server.
type UserOut struct {
	ID          int64     `json:"id" doc:"User ID."`
	Username    string    `json:"username" doc:"Unique login name (case-insensitive)."`
	DisplayName string    `json:"display_name" doc:"Name shown in the UI."`
	Email       string    `json:"email" doc:"Email address, empty when unknown."`
	IsAdmin     bool      `json:"is_admin" doc:"Whether the user holds the admin scope."`
	IsBootstrap bool      `json:"is_bootstrap" doc:"Whether this is the first-run admin, who can never be demoted or disabled."`
	Disabled    bool      `json:"disabled" doc:"Whether sign-in and every credential of the user is refused."`
	Units       string    `json:"units" enum:"metric,imperial" doc:"Preferred display units."`
	OIDC        bool      `json:"oidc" doc:"Whether the account is linked to an OIDC identity."`
	CreatedAt   time.Time `json:"created_at" doc:"Creation time, UTC."`
}

func userOut(u store.User) UserOut {
	return UserOut{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Email: u.Email,
		IsAdmin: u.IsAdmin, IsBootstrap: u.IsBootstrap, Disabled: u.Disabled, Units: u.Units,
		OIDC: u.Iss != "", CreatedAt: u.CreatedAt}
}

type listUsersIn struct{}

type listUsersOut struct {
	Body []UserOut
}

type patchUserIn struct {
	ID   int64 `path:"id" doc:"User ID."`
	Body struct {
		IsAdmin  *bool `json:"is_admin,omitempty" doc:"Grant or revoke the admin scope."`
		Disabled *bool `json:"disabled,omitempty" doc:"Disable or re-enable the account. Disabling deletes the user's sessions."`
	}
}

type userBody struct {
	Body UserOut
}

func (d Deps) registerUsers(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/users", ID: "listUsers", Tag: "users",
		Summary:     "List users",
		Description: "Returns every user account, ordered by ID.",
		Scope:       auth.ScopeAdmin},
		func(ctx context.Context, _ *listUsersIn) (*listUsersOut, error) {
			us, err := d.Store.ListUsers(ctx)
			if err != nil {
				return nil, err
			}
			out := &listUsersOut{Body: make([]UserOut, 0, len(us))}
			for _, u := range us {
				out.Body = append(out.Body, userOut(u))
			}
			return out, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/users/{id}", ID: "updateUser", Tag: "users",
		Summary: "Update a user",
		Description: "Changes a user's admin or disabled flag. Disabling a user deletes all of that user's sessions. " +
			"The bootstrap admin cannot be demoted or disabled (409 `bootstrap_protected`), and the last enabled admin " +
			"cannot be removed (409 `last_admin`).",
		Scope: auth.ScopeAdmin, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *patchUserIn) (*userBody, error) {
			u, err := d.Store.UpdateUserFlags(ctx, in.ID, in.Body.IsAdmin, in.Body.Disabled)
			switch {
			case errors.Is(err, store.ErrNotFound):
				return nil, apiErr(http.StatusNotFound, "not_found", "no such user")
			case errors.Is(err, store.ErrBootstrap):
				return nil, apiErr(http.StatusConflict, "bootstrap_protected", "the bootstrap admin cannot be demoted or disabled")
			case errors.Is(err, store.ErrLastAdmin):
				return nil, apiErr(http.StatusConflict, "last_admin", "at least one enabled admin must remain")
			case err != nil:
				return nil, err
			}
			if in.Body.Disabled != nil && *in.Body.Disabled {
				if _, err := d.Store.DeleteUserSessions(ctx, u.ID); err != nil {
					return nil, err
				}
			}
			return &userBody{Body: userOut(u)}, nil
		})
}
