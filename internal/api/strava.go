package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/store"
)

// StravaLinkOut is a rider's Strava connection. It never carries a secret
// or token.
type StravaLinkOut struct {
	Available       bool       `json:"available" doc:"Whether Strava app credentials (the rider's own or the shared ones from the settings) are configured, so the rider can connect."`
	UsesOwnApp      bool       `json:"uses_own_app" doc:"Whether this rider uses their own Strava API application instead of the shared one."`
	ClientID        string     `json:"client_id" doc:"The rider's own Strava API client ID; empty when the shared application is used."`
	ClientSecretSet bool       `json:"client_secret_set" doc:"Whether the rider's own client secret is stored. The secret itself is never returned."`
	Connected       bool       `json:"connected" doc:"Whether the rider has authorised Wattson at Strava."`
	AthleteID       int64      `json:"athlete_id" doc:"Strava athlete ID while connected; 0 otherwise."`
	AthleteName     string     `json:"athlete_name" doc:"Strava athlete name while connected; empty otherwise."`
	Scope           string     `json:"scope" doc:"Comma-separated scopes Strava granted while connected, such as read,activity:write; empty otherwise."`
	AutoUpload      bool       `json:"auto_upload" doc:"Whether finished rides are uploaded to Strava automatically."`
	ConnectedAt     *time.Time `json:"connected_at" nullable:"true" doc:"When the rider connected, UTC; null while not connected."`
	CallbackDomain  string     `json:"callback_domain" doc:"Host (no port) to enter as the Authorization Callback Domain in the Strava API application."`
	RedirectURI     string     `json:"redirect_uri" doc:"OAuth redirect URI this server sends to Strava: the public URL setting, else the address of this request, plus /api/strava/callback."`
}

type stravaLinkBody struct{ Body StravaLinkOut }

type stravaPatchIn struct {
	ID   int64 `path:"id" doc:"Rider ID."`
	Body struct {
		ClientID     *string `json:"client_id,omitempty" pattern:"^[0-9]{0,12}$" doc:"The rider's own Strava API client ID: digits only, at most 12. An empty string returns the rider to the shared application and removes the own secret. A changed value disconnects the rider."`
		ClientSecret *string `json:"client_secret,omitempty" maxLength:"256" doc:"The rider's own Strava client secret. Omit to keep the stored secret; a changed value disconnects the rider. Must be sent together with client_id (400 otherwise). Never returned."`
		AutoUpload   *bool   `json:"auto_upload,omitempty" doc:"Upload finished rides to Strava automatically."`
	}
}

var errNoStrava = apiErrPublic(http.StatusServiceUnavailable, "no_strava", "the Strava service is not available")

// stravaRedirectURI is the OAuth redirect URI for this request.
func (d Deps) stravaRedirectURI(ctx context.Context, r *http.Request) string {
	pub := ""
	var trusted []netip.Prefix
	if s, err := d.Settings.Get(ctx); err == nil {
		pub, trusted = s.General.PublicURL, trustedProxies(s)
	}
	return externalBaseURL(r, pub, trusted) + "/api/strava/callback"
}

func (d Deps) stravaLinkOut(ctx context.Context, r *http.Request, l store.StravaLink) StravaLinkOut {
	_, _, err := d.Strava.AppFor(ctx, l.RiderID)
	redirect := d.stravaRedirectURI(ctx, r)
	out := StravaLinkOut{Available: err == nil, UsesOwnApp: l.ClientID != "", ClientID: l.ClientID,
		ClientSecretSet: l.ClientSecretEnc != "", Connected: l.Connected(), AutoUpload: l.AutoUpload,
		ConnectedAt: l.ConnectedAt, RedirectURI: redirect}
	if u, err := url.Parse(redirect); err == nil {
		out.CallbackDomain = u.Hostname()
	}
	if l.Connected() {
		out.AthleteID, out.AthleteName, out.Scope = l.AthleteID, l.AthleteName, l.Scope
	}
	return out
}

// ownedRiderStrava returns the link of one of the caller's riders.
func (d Deps) ownedRiderStrava(ctx context.Context, riderID int64) (store.StravaLink, error) {
	if d.Strava == nil {
		return store.StravaLink{}, errNoStrava
	}
	ident, _ := auth.FromContext(ctx)
	if _, err := d.Store.Rider(ctx, ident.User.ID, riderID); errors.Is(err, store.ErrNotFound) {
		return store.StravaLink{}, errNoRider()
	} else if err != nil {
		return store.StravaLink{}, err
	}
	return d.Store.StravaLink(ctx, riderID)
}

func (d Deps) registerStrava(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/riders/{id}/strava", ID: "getRiderStrava", Tag: "strava",
		Summary: "Get a rider's Strava link",
		Description: "Returns whether the rider can and does use Strava, the rider's own app settings and the redirect URI " +
			"to register at Strava. No secret or token is returned. A rider owned by someone else returns 404.",
		Scope: auth.ScopeRead, Errors: []int{http.StatusServiceUnavailable}},
		func(ctx context.Context, in *riderIDIn) (*stravaLinkBody, error) {
			l, err := d.ownedRiderStrava(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			return &stravaLinkBody{Body: d.stravaLinkOut(ctx, httpRequest(ctx), l)}, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/riders/{id}/strava", ID: "updateRiderStrava", Tag: "strava",
		Summary: "Update a rider's Strava link",
		Description: "Sets the rider's own Strava API application (`client_id`, `client_secret`) and the `auto_upload` flag; " +
			"omitted fields are unchanged. Changing the client ID or secret disconnects the rider, because tokens belong to " +
			"the old application. A rider owned by someone else returns 404.",
		Scope: auth.ScopeControl, Errors: []int{http.StatusServiceUnavailable}},
		func(ctx context.Context, in *stravaPatchIn) (*stravaLinkBody, error) {
			if _, err := d.ownedRiderStrava(ctx, in.ID); err != nil {
				return nil, err
			}
			if in.Body.ClientSecret != nil && in.Body.ClientID == nil {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "client_secret: send client_id together with it")
			}
			l, err := d.Strava.UpdateLink(ctx, in.ID, in.Body.ClientID, in.Body.ClientSecret, in.Body.AutoUpload)
			if err != nil {
				return nil, err
			}
			return &stravaLinkBody{Body: d.stravaLinkOut(ctx, httpRequest(ctx), l)}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/riders/{id}/strava/disconnect", ID: "disconnectRiderStrava", Tag: "strava",
		Summary: "Disconnect a rider from Strava",
		Description: "Revokes Wattson's access at Strava (best effort: a Strava failure does not stop it) and forgets the " +
			"athlete and tokens. The rider's own app credentials and `auto_upload` stay. A rider owned by someone else returns 404.",
		Scope: auth.ScopeControl, Status: http.StatusNoContent, Errors: []int{http.StatusServiceUnavailable}},
		func(ctx context.Context, in *riderIDIn) (*noContent, error) {
			if _, err := d.ownedRiderStrava(ctx, in.ID); err != nil {
				return nil, err
			}
			if err := d.Strava.Disconnect(ctx, in.ID); err != nil {
				return nil, err
			}
			return &noContent{}, nil
		})
}
