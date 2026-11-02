package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/store"
)

const defaultStravaReturn = "/riders"

func init() {
	registerSpec(func() []opSpec {
		return []opSpec{
			{Method: http.MethodGet, Path: "/api/riders/{id}/strava/connect", ID: "connectRiderStrava", Tag: "strava",
				Summary: "Start connecting a rider to Strava",
				Description: "Redirects (302) the browser to Strava's authorization page for one of the caller's riders, using " +
					"the rider's own Strava application or the shared one, and the scopes `read` and `activity:write`. The signed " +
					"state is bound to the caller and the rider and valid for 10 minutes. `return_to` must be a same-origin path " +
					"(default `/riders`). Without configured credentials the redirect goes to `<return_to>?strava=error&reason=not_configured` " +
					"instead. A rider owned by someone else returns 404.",
				Params: []p{
					{Name: "id", Doc: "Rider ID."},
					{Name: "return_to", Doc: "Same-origin path to open when the flow ends, for example `/riders`. Default `/riders`.", Type: "string", Query: true},
				},
				Scope: auth.ScopeControl, Status: http.StatusFound, Errors: []int{http.StatusNotFound}},
			{Method: http.MethodGet, Path: "/api/strava/callback", ID: "stravaCallback", Tag: "strava",
				Summary: "Finish connecting a rider to Strava",
				Description: "The redirect URI registered at Strava. Needs the session cookie of the user who started the flow; " +
					"the signed state must belong to that user. Always answers 302 to the `return_to` path of the state: " +
					"`?strava=connected` on success, otherwise `?strava=error&reason=<reason>` where reason is one of `denied` " +
					"(the user refused), `state` (missing, tampered, expired or another user's state, or the rider is gone), " +
					"`scope` (Strava did not grant `read` and `activity:write`) or `exchange_failed`. Without a session it " +
					"redirects to `/login`.",
				Params: []p{
					{Name: "code", Doc: "Authorization code from Strava.", Type: "string", Query: true},
					{Name: "state", Doc: "State issued by the connect redirect.", Type: "string", Query: true},
					{Name: "scope", Doc: "Comma-separated scopes the user granted.", Type: "string", Query: true},
					{Name: "error", Doc: "Error code from Strava, such as access_denied.", Type: "string", Query: true},
				},
				Status: http.StatusFound, NoStdErrors: true, Errors: []int{http.StatusUnauthorized}},
		}
	})
}

// registerStravaFlow mounts the browser redirects of the Strava connect flow.
func (d Deps) registerStravaFlow(r chi.Router) {
	r.Get("/api/riders/{id}/strava/connect", d.handleStravaConnect)
	r.Get("/api/strava/callback", d.handleStravaCallback)
}

// stravaResult appends the flow outcome to the same-origin path returnTo.
func stravaResult(returnTo, outcome string) string {
	u, err := url.Parse(returnTo)
	if err != nil {
		return defaultStravaReturn + "?" + outcome
	}
	q := outcome
	if u.RawQuery != "" {
		q = u.RawQuery + "&" + outcome
	}
	return u.EscapedPath() + "?" + q
}

func stravaFail(returnTo, reason string) string {
	return stravaResult(returnTo, "strava=error&reason="+reason)
}

func returnToParam(v string) string {
	if v == "" {
		return defaultStravaReturn
	}
	return sanitizeReturnTo(v)
}

func (d Deps) handleStravaConnect(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.FromContext(r.Context())
	switch {
	case !ok:
		writeEnvelope(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	case !ident.Has(auth.ScopeControl):
		writeEnvelope(w, http.StatusForbidden, "forbidden", "missing required scope: control")
		return
	case d.Strava == nil:
		writeEnvelope(w, http.StatusServiceUnavailable, "no_strava", "the Strava service is not available")
		return
	}
	riderID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeEnvelope(w, http.StatusBadRequest, "invalid_request", "id: must be an integer")
		return
	}
	if _, err := d.Store.Rider(r.Context(), ident.User.ID, riderID); errors.Is(err, store.ErrNotFound) {
		writeEnvelope(w, http.StatusNotFound, "not_found", "no such rider")
		return
	} else if err != nil {
		d.Logger.Error("strava connect: load rider", "err", err)
		writeEnvelope(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	returnTo := returnToParam(r.URL.Query().Get("return_to"))
	loc, err := d.Strava.ConnectURL(r.Context(), riderID, ident.User.ID, d.stravaRedirectURI(r.Context(), r), returnTo)
	switch {
	case errors.Is(err, strava.ErrNotConfigured):
		redirectTo(w, stravaFail(returnTo, "not_configured"))
	case err != nil:
		d.Logger.Error("strava connect", "rider", riderID, "err", err)
		writeEnvelope(w, http.StatusInternalServerError, "internal_error", "internal error")
	default:
		redirectTo(w, loc)
	}
}

func (d Deps) handleStravaCallback(w http.ResponseWriter, r *http.Request) {
	ident, ok := auth.FromContext(r.Context())
	if !ok {
		redirectTo(w, "/login")
		return
	}
	if ident.Via != "session" {
		writeEnvelope(w, http.StatusUnauthorized, "unauthorized", "a session is required")
		return
	}
	if d.Strava == nil {
		writeEnvelope(w, http.StatusServiceUnavailable, "no_strava", "the Strava service is not available")
		return
	}
	q := r.URL.Query()
	cs, err := d.Strava.ReadState(q.Get("state"), ident.User.ID)
	if err != nil {
		d.Logger.Warn("strava callback refused", "reason", "state", "user", ident.User.ID, "err", err)
		redirectTo(w, stravaFail(defaultStravaReturn, "state"))
		return
	}
	returnTo := sanitizeReturnTo(cs.ReturnTo)
	if q.Get("error") != "" || q.Get("code") == "" {
		d.Logger.Info("strava connect not granted", "rider", cs.RiderID, "error", truncate(q.Get("error"), 100))
		redirectTo(w, stravaFail(returnTo, "denied"))
		return
	}
	_, err = d.Strava.Complete(r.Context(), q.Get("state"), q.Get("code"), q.Get("scope"),
		d.stravaRedirectURI(r.Context(), r), ident.User.ID)
	switch {
	case err == nil:
		redirectTo(w, stravaResult(returnTo, "strava=connected"))
	case errors.Is(err, strava.ErrState):
		redirectTo(w, stravaFail(returnTo, "state"))
	case errors.Is(err, strava.ErrScope):
		d.Logger.Info("strava connect refused", "rider", cs.RiderID, "reason", "scope", "granted", truncate(q.Get("scope"), 100))
		redirectTo(w, stravaFail(returnTo, "scope"))
	default:
		d.Logger.Warn("strava connect failed", "rider", cs.RiderID, "reason", "exchange_failed", "err", err)
		redirectTo(w, stravaFail(returnTo, "exchange_failed"))
	}
}
