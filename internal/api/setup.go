package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

// SetupStatus is the body of GET /api/setup/status.
type SetupStatus struct {
	NeedsSetup bool `json:"needs_setup" doc:"True while no user exists and the setup wizard must run."`
}

type setupStatusIn struct{}

type setupStatusOut struct {
	Body SetupStatus
}

type setupIn struct {
	Body struct {
		Username    string `json:"username" pattern:"^[A-Za-z0-9._-]{3,64}$" doc:"Login name of the first admin: 3 to 64 letters, digits, dot, underscore or hyphen."`
		Password    string `json:"password" doc:"Password of the first admin, 10 to 256 bytes."`
		DisplayName string `json:"display_name,omitempty" maxLength:"100" doc:"Name shown in the UI. Defaults to the username."`
		PublicURL   string `json:"public_url,omitempty" doc:"External base URL of this server (http or https, no path), used for OIDC redirects, CSRF checks and the Secure cookie flag."`
	}
}

type setupOut struct {
	SetCookie http.Cookie `header:"Set-Cookie" doc:"Session cookie for the new admin."`
	Body      UserOut
}

func (d Deps) registerSetup(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/setup/status", ID: "getSetupStatus", Tag: "setup",
		Summary:     "Setup status",
		Description: "Reports whether first-run setup is still needed. No authentication required."},
		func(ctx context.Context, _ *setupStatusIn) (*setupStatusOut, error) {
			n, err := d.Store.CountUsers(ctx)
			if err != nil {
				return nil, err
			}
			return &setupStatusOut{Body: SetupStatus{NeedsSetup: n == 0}}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/setup", ID: "runSetup", Tag: "setup",
		Summary: "Create the first admin",
		Description: "Creates the bootstrap admin, optionally saves the public URL, and signs the new admin in " +
			"with a session cookie. If saving the public URL fails after the admin was created, the failure is logged and " +
			"the admin is still created. Fails with 409 `already_setup` once any user exists. No authentication required.",
		Status: http.StatusCreated, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *setupIn) (*setupOut, error) {
			b := in.Body
			if err := auth.ValidatePassword(b.Password); err != nil {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", err.Error())
			}
			if n, err := d.Store.CountUsers(ctx); err != nil {
				return nil, err
			} else if n > 0 {
				return nil, errAlreadySetup()
			}
			if b.PublicURL != "" {
				cur, err := d.Settings.Get(ctx)
				if err != nil {
					return nil, err
				}
				cur.General.PublicURL = b.PublicURL
				if err := cur.Validate(); err != nil {
					return nil, settingsErr(err)
				}
			}
			hash, err := auth.HashPassword(ctx, b.Password)
			if err != nil {
				return nil, err
			}
			display := b.DisplayName
			if display == "" {
				display = b.Username
			}
			u, err := d.Store.CreateBootstrapAdmin(ctx, b.Username, display, hash)
			switch {
			case errors.Is(err, store.ErrAlreadySetup):
				return nil, errAlreadySetup()
			case err != nil:
				return nil, err
			}
			if b.PublicURL != "" {
				_, err := d.Settings.Update(ctx, func(s *settings.Settings) error {
					s.General.PublicURL = b.PublicURL
					return nil
				})
				if err != nil { // validated above; the admin exists, so do not fail the call
					loggerFrom(ctx).Error("setup: saving public_url failed", "err", err)
				}
			}
			cookie, err := d.startSession(ctx, httpRequest(ctx), u.ID)
			if err != nil {
				return nil, err
			}
			return &setupOut{SetCookie: cookie, Body: userOut(u)}, nil
		})
}

func errAlreadySetup() error {
	return apiErr(http.StatusConflict, "already_setup", "setup has already been completed")
}

// settingsErr maps a settings validation failure to a 400.
func settingsErr(err error) error {
	var ve *settings.ValidationError
	if errors.As(err, &ve) {
		return apiErr(http.StatusBadRequest, "invalid_request", ve.Error())
	}
	return err
}
