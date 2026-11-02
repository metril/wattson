package api

import (
	"context"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

const (
	loginMaxFails = 10 // failed attempts per remote IP in loginWindow
	loginWindow   = 10 * time.Minute
)

// MeOut describes the caller.
type MeOut struct {
	User   UserOut  `json:"user" doc:"The authenticated user."`
	Scopes []string `json:"scopes" doc:"Scopes held by this credential: read, control, admin."`
	Via    string   `json:"via" enum:"session,token" doc:"How the caller authenticated."`
}

// ProviderButton is one external sign-in option.
type ProviderButton struct {
	Slug        string `json:"slug" doc:"Provider slug."`
	DisplayName string `json:"display_name" doc:"Text for the sign-in button."`
	LoginURL    string `json:"login_url" doc:"Path that starts the sign-in redirect."`
}

// ProvidersOut lists the ways to sign in.
type ProvidersOut struct {
	NeedsSetup bool             `json:"needs_setup" doc:"True while first-run setup is still needed."`
	LocalLogin bool             `json:"local_login" doc:"Whether username and password sign-in is enabled."`
	Providers  []ProviderButton `json:"providers" doc:"Configured OIDC providers."`
}

type loginIn struct {
	Body struct {
		Username string `json:"username" minLength:"1" doc:"Login name (case-insensitive)."`
		Password string `json:"password" doc:"Password."`
	}
}

type cookieOut struct {
	SetCookie http.Cookie `header:"Set-Cookie" doc:"Session cookie."`
}

type noIn struct{}

type meOut struct {
	Body MeOut
}

type passwordIn struct {
	Body struct {
		CurrentPassword string `json:"current_password" doc:"The current password."`
		NewPassword     string `json:"new_password" doc:"The new password, 10 to 256 bytes."`
	}
}

type providersOut struct {
	Body ProvidersOut
}

// clientIP is the host of r.RemoteAddr. X-Forwarded-For is never consulted:
// it is client-controlled unless a trusted proxy rewrites it, and Wattson has
// no trusted-proxy setting.
func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// newDummyHash returns a valid argon2 hash that is verified for unknown
// users so that both outcomes cost one argon2 run.
func newDummyHash() (string, error) {
	return auth.HashPassword(context.Background(), "wattson-dummy-password")
}

func rateLimited(ctx context.Context, retry time.Duration) error {
	secs := max(int(math.Ceil(retry.Seconds())), 1)
	setHeader(ctx, "Retry-After", strconv.Itoa(secs))
	return apiErr(http.StatusTooManyRequests, "rate_limited", "too many failed attempts; try again later")
}

func errInvalidCreds() error {
	return apiErr(http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
}

// secureCookie reports whether the session cookie must carry Secure.
func secureCookie(r *http.Request, publicURL string) bool {
	if r != nil {
		if r.TLS != nil {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
			return true
		}
	}
	u, err := url.Parse(publicURL)
	return err == nil && strings.EqualFold(u.Scheme, "https")
}

func sessionCookieFor(r *http.Request, publicURL, value string, maxAge int) http.Cookie {
	return http.Cookie{Name: auth.SessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r, publicURL)}
}

// startSession creates a session for userID with a fixed expiry and returns
// its cookie.
func (d Deps) startSession(ctx context.Context, r *http.Request, userID int64) (http.Cookie, error) {
	s, err := d.Settings.Get(ctx)
	if err != nil {
		return http.Cookie{}, err
	}
	plain, hash := auth.GenerateSecret()
	ttl := time.Duration(s.Auth.SessionTTLHours) * time.Hour
	now := d.Now()
	if err := d.Store.CreateSession(ctx, hash, userID, now, now.Add(ttl)); err != nil {
		return http.Cookie{}, err
	}
	return sessionCookieFor(r, s.General.PublicURL, plain, int(ttl/time.Second)), nil
}

func (d Deps) registerAuthn(api huma.API) {
	dummy, err := newDummyHash()
	if err != nil {
		panic("api: cannot compute the dummy password hash: " + err.Error())
	}
	// Login attempts are limited per remote IP only (not per username, which
	// would let a third party lock a victim out): 10 failures in 10 minutes.
	// A successful login does not reset the count, so a valid account cannot
	// be used to restart a password spray from the same address.
	limiter := auth.NewLimiter(loginMaxFails, loginWindow)
	// Password-change guessing by a signed-in user, keyed by user ID.
	pwLimit := auth.NewLimiter(loginMaxFails, loginWindow)

	register(api, route{Method: http.MethodPost, Path: "/api/auth/login", ID: "login", Tag: "auth",
		Summary: "Sign in with a password",
		Description: "Verifies a local username and password and sets the session cookie. Attempts are rate limited per " +
			"remote IP address (the connection's address; X-Forwarded-For is ignored): after 10 failures in 10 minutes " +
			"every attempt from that address, whatever the username, returns 429 `rate_limited` with a `Retry-After` " +
			"header in seconds. Unknown user, wrong password, " +
			"disabled user and password-less (OIDC) users all return the same 401 `invalid_credentials`. Returns 403 " +
			"`local_login_disabled` when password sign-in is turned off and the user is not the bootstrap admin. " +
			"No authentication required.",
		Status: http.StatusNoContent, Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}},
		func(ctx context.Context, in *loginIn) (*cookieOut, error) {
			r := httpRequest(ctx)
			res, retry := limiter.Allow(clientIP(r))
			if res == nil {
				return nil, rateLimited(ctx, retry)
			}
			defer res.Release()

			u, err := d.Store.UserByUsername(ctx, in.Body.Username)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			known := err == nil && u.PasswordHash != "" && !u.Disabled && len(in.Body.Password) <= 256
			hash := dummy
			if known {
				hash = u.PasswordHash
			}
			match, err := auth.VerifyPassword(ctx, hash, in.Body.Password)
			if err != nil {
				return nil, err
			}
			if !known || !match {
				res.Fail()
				return nil, errInvalidCreds()
			}
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			res.Release() // a valid password does not reset the per-IP failure count
			if !s.Auth.AllowLocalLogin && !u.IsBootstrap {
				return nil, apiErr(http.StatusForbidden, "local_login_disabled", "password sign-in is disabled; use single sign-on")
			}
			cookie, err := d.startSession(ctx, r, u.ID)
			if err != nil {
				return nil, err
			}
			return &cookieOut{SetCookie: cookie}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/auth/logout", ID: "logout", Tag: "auth",
		Summary:     "Sign out",
		Description: "Deletes the current session, if any, and clears the session cookie. No authentication required.",
		Status:      http.StatusNoContent},
		func(ctx context.Context, _ *noIn) (*cookieOut, error) {
			r := httpRequest(ctx)
			if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
				if err := d.Store.DeleteSession(ctx, auth.HashSecret(c.Value)); err != nil {
					return nil, err
				}
			}
			pub := ""
			if s, err := d.Settings.Get(ctx); err == nil {
				pub = s.General.PublicURL
			}
			return &cookieOut{SetCookie: sessionCookieFor(r, pub, "", -1)}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/auth/me", ID: "getMe", Tag: "auth",
		Summary:     "Current identity",
		Description: "Returns the authenticated user, the scopes of the credential in use, and whether it is a session or a token.",
		Scope:       auth.ScopeRead},
		func(ctx context.Context, _ *noIn) (*meOut, error) {
			id, _ := auth.FromContext(ctx)
			scopes := make([]string, 0, len(id.Scopes))
			for _, s := range id.Scopes {
				scopes = append(scopes, string(s))
			}
			return &meOut{Body: MeOut{User: userOut(id.User), Scopes: scopes, Via: id.Via}}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/auth/password", ID: "changePassword", Tag: "auth",
		Summary: "Change own password",
		Description: "Changes the signed-in user's password. Session only: an API token gets 403 `session_required`. " +
			"A wrong current password returns 400 `invalid_current_password`. All of the user's sessions are deleted and " +
			"a fresh session cookie is set.",
		Scope: auth.ScopeRead, Status: http.StatusNoContent, Errors: []int{http.StatusTooManyRequests}},
		func(ctx context.Context, in *passwordIn) (*cookieOut, error) {
			id, _ := auth.FromContext(ctx)
			if id.Via != "session" {
				return nil, errSessionRequired()
			}
			if err := auth.ValidatePassword(in.Body.NewPassword); err != nil {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", err.Error())
			}
			key := "pw|" + strconv.FormatInt(id.User.ID, 10)
			res, retry := pwLimit.Allow(key)
			if res == nil {
				return nil, rateLimited(ctx, retry)
			}
			defer res.Release()
			known := id.User.PasswordHash != "" && len(in.Body.CurrentPassword) <= 256
			hash := dummy
			if known {
				hash = id.User.PasswordHash
			}
			match, err := auth.VerifyPassword(ctx, hash, in.Body.CurrentPassword)
			if err != nil {
				return nil, err
			}
			if !known || !match {
				res.Fail()
				return nil, apiErr(http.StatusBadRequest, "invalid_current_password", "current password is incorrect")
			}
			res.Success()
			nh, err := auth.HashPassword(ctx, in.Body.NewPassword)
			if err != nil {
				return nil, err
			}
			if err := d.Store.SetPassword(ctx, id.User.ID, nh); err != nil {
				return nil, err
			}
			// Create the new session first, then drop every other one, so a
			// session opened concurrently with the change does not survive it.
			cookie, err := d.startSession(ctx, httpRequest(ctx), id.User.ID)
			if err != nil {
				// The password has changed but no replacement session exists:
				// lock out every old session rather than leave them valid.
				if _, derr := d.Store.DeleteUserSessions(context.WithoutCancel(ctx), id.User.ID); derr != nil {
					loggerFrom(ctx).Error("revoke sessions after failed password change", "err", derr)
				}
				return nil, err
			}
			if _, err := d.Store.DeleteOtherSessions(ctx, id.User.ID, auth.HashSecret(cookie.Value)); err != nil {
				return nil, err
			}
			return &cookieOut{SetCookie: cookie}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/auth/providers", ID: "listAuthProviders", Tag: "auth",
		Summary:     "Sign-in options",
		Description: "Lists the ways to sign in: whether setup is pending, whether password sign-in is enabled, and the enabled OIDC providers ordered by display name. No authentication required."},
		func(ctx context.Context, _ *noIn) (*providersOut, error) {
			n, err := d.Store.CountUsers(ctx)
			if err != nil {
				return nil, err
			}
			s, err := d.Settings.Get(ctx)
			if err != nil {
				return nil, err
			}
			ps, err := d.Store.ListOIDCProviders(ctx) // ordered by display name
			if err != nil {
				return nil, err
			}
			buttons := []ProviderButton{}
			for _, p := range ps {
				if p.Enabled {
					buttons = append(buttons, ProviderButton{Slug: p.Slug, DisplayName: p.DisplayName,
						LoginURL: "/api/auth/oidc/" + p.Slug + "/login"})
				}
			}
			return &providersOut{Body: ProvidersOut{NeedsSetup: n == 0, LocalLogin: s.Auth.AllowLocalLogin,
				Providers: buttons}}, nil
		})
}

func errSessionRequired() error {
	return apiErr(http.StatusForbidden, "session_required", "this operation requires a signed-in session, not an API token")
}
