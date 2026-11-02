package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/metril/wattson/internal/auth"
	"github.com/metril/wattson/internal/oidcauth"
	"github.com/metril/wattson/internal/store"
)

// Defaults for a new provider, matching the oidc_providers column defaults.
const (
	defaultOIDCScopes      = "openid profile email groups"
	defaultOIDCGroupsClaim = "groups"
)

// ProviderOut is an OIDC provider as returned to admins. The client secret
// is sealed at rest and never returned.
type ProviderOut struct {
	ID              int64     `json:"id" doc:"Provider ID."`
	Slug            string    `json:"slug" doc:"URL-safe identifier, unique and immutable. Part of the login and callback URLs."`
	DisplayName     string    `json:"display_name" doc:"Text for the sign-in button."`
	DiscoveryURL    string    `json:"discovery_url" doc:"OIDC discovery URL as entered: the issuer URL or the full .well-known/openid-configuration URL. The issuer itself is read from the discovery document at login."`
	ClientID        string    `json:"client_id" doc:"OAuth client ID registered at the provider."`
	HasClientSecret bool      `json:"has_client_secret" doc:"Whether a client secret is stored. The secret itself is never returned."`
	ClientSecretSet bool      `json:"client_secret_set" doc:"Same as has_client_secret."`
	Scopes          string    `json:"scopes" doc:"Space-separated scopes requested in addition to the always-requested openid, profile and email."`
	GroupsClaim     string    `json:"groups_claim" doc:"Name of the ID token or userinfo claim that lists the user's groups; empty disables group handling."`
	AllowedGroups   []string  `json:"allowed_groups" doc:"When not empty, only users in at least one of these groups may sign in (case-insensitive)."`
	AllowedEmails   []string  `json:"allowed_emails" doc:"When not empty, only these verified email addresses may sign in (case-insensitive)."`
	AdminGroup      string    `json:"admin_group" doc:"Members of this group are admins, and others lose admin on their next sign-in. Empty leaves admin status to the local user list. The bootstrap admin and the last admin are never demoted."`
	AutoCreateUsers bool      `json:"auto_create_users" doc:"Whether a first sign-in creates a local user."`
	LinkByEmail     bool      `json:"link_by_email" doc:"Whether a first sign-in links to an existing local user with the same email, when the provider asserts the email is verified and exactly one local user has it."`
	Enabled         bool      `json:"enabled" doc:"Whether users can sign in with this provider."`
	RedirectURI     string    `json:"redirect_uri" doc:"Redirect (callback) URL to register at the provider. Built from the public URL setting, or from the request when that is empty."`
	CreatedAt       time.Time `json:"created_at" doc:"Creation time, UTC."`
	UpdatedAt       time.Time `json:"updated_at" doc:"Last change time, UTC."`
}

func (d Deps) providerOut(ctx context.Context, r *http.Request, p store.OIDCProvider) ProviderOut {
	set := p.ClientSecretEnc != ""
	return ProviderOut{ID: p.ID, Slug: p.Slug, DisplayName: p.DisplayName, DiscoveryURL: p.IssuerURL,
		ClientID: p.ClientID, HasClientSecret: set, ClientSecretSet: set, Scopes: p.Scopes,
		GroupsClaim: p.GroupsClaim, AllowedGroups: nonNil(p.AllowedGroups), AllowedEmails: nonNil(p.AllowedEmails),
		AdminGroup: p.AdminGroup, AutoCreateUsers: p.AutoCreateUsers, LinkByEmail: p.LinkByEmail,
		Enabled: p.Enabled, RedirectURI: d.redirectURI(ctx, r, p.Slug), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// redirectURI is the callback URL for slug: the public URL setting if set,
// else the request's own origin.
func (d Deps) redirectURI(ctx context.Context, r *http.Request, slug string) string {
	pub := ""
	if s, err := d.Settings.Get(ctx); err == nil {
		pub = s.General.PublicURL
	}
	return externalBaseURL(r, pub) + "/api/auth/oidc/" + slug + "/callback"
}

// externalBaseURL is the origin browsers use to reach this server: the
// configured public URL, else the request's scheme and Host. X-Forwarded-Host
// is ignored (any client can set it); behind a proxy that rewrites Host, set
// the public URL.
func externalBaseURL(r *http.Request, publicURL string) string {
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}
	scheme := "http"
	if secureCookie(r, "") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// providerSecretAAD binds a sealed client secret to its provider.
func providerSecretAAD(slug string) string { return "oidc." + slug + ".client_secret" }

func validDiscoveryURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return apiErr(http.StatusBadRequest, "invalid_request", "discovery_url: must be an absolute http or https URL")
	}
	return nil
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ProviderParams are the optional provider fields shared by create and update.
type ProviderParams struct {
	Scopes          *string  `json:"scopes,omitempty" maxLength:"512" doc:"Space-separated scopes requested in addition to openid, profile and email. Default \"openid profile email groups\"."`
	GroupsClaim     *string  `json:"groups_claim,omitempty" maxLength:"64" doc:"Claim that lists the user's groups. Default \"groups\"; empty disables group handling."`
	AllowedGroups   []string `json:"allowed_groups,omitempty" maxItems:"100" doc:"Only members of one of these groups may sign in; empty allows everyone. Default empty."`
	AllowedEmails   []string `json:"allowed_emails,omitempty" maxItems:"100" doc:"Only these verified email addresses may sign in; empty allows everyone. Default empty."`
	AdminGroup      *string  `json:"admin_group,omitempty" maxLength:"128" doc:"Group whose members are admins; empty leaves admin status unmanaged. Default empty."`
	AutoCreateUsers *bool    `json:"auto_create_users,omitempty" doc:"Create a local user on first sign-in. Default true."`
	LinkByEmail     *bool    `json:"link_by_email,omitempty" doc:"Link a first sign-in to the local user with the same verified email. Default false."`
	Enabled         *bool    `json:"enabled,omitempty" doc:"Allow sign-in with this provider. Default true."`
}

func (q ProviderParams) apply(p *store.OIDCProvider) {
	if q.Scopes != nil {
		p.Scopes = strings.Join(strings.Fields(*q.Scopes), " ")
	}
	if q.GroupsClaim != nil {
		p.GroupsClaim = strings.TrimSpace(*q.GroupsClaim)
	}
	if q.AllowedGroups != nil { // nil is absent; [] clears the list
		p.AllowedGroups = cleanList(q.AllowedGroups)
	}
	if q.AllowedEmails != nil {
		p.AllowedEmails = cleanList(q.AllowedEmails)
	}
	if q.AdminGroup != nil {
		p.AdminGroup = strings.TrimSpace(*q.AdminGroup)
	}
	set(&p.AutoCreateUsers, q.AutoCreateUsers)
	set(&p.LinkByEmail, q.LinkByEmail)
	set(&p.Enabled, q.Enabled)
}

type providerBody struct {
	Body ProviderOut
}

type providersBody struct {
	Body []ProviderOut
}

type providerIDIn struct {
	ID int64 `path:"id" doc:"Provider ID."`
}

type createProviderIn struct {
	Body struct {
		Slug         string `json:"slug" pattern:"^[a-z0-9][a-z0-9-]{0,31}$" doc:"URL-safe identifier: 1 to 32 lowercase letters, digits or hyphens, not starting with a hyphen. Cannot be changed later."`
		DisplayName  string `json:"display_name" minLength:"1" maxLength:"64" doc:"Text for the sign-in button, 1 to 64 characters."`
		DiscoveryURL string `json:"discovery_url" minLength:"1" maxLength:"512" doc:"OIDC discovery URL: the issuer URL or the full .well-known/openid-configuration URL."`
		ClientID     string `json:"client_id" minLength:"1" maxLength:"256" doc:"OAuth client ID registered at the provider."`
		ClientSecret string `json:"client_secret" minLength:"1" maxLength:"1024" doc:"OAuth client secret. Stored sealed and never returned."`
		ProviderParams
	}
}

type patchProviderIn struct {
	ID   int64 `path:"id" doc:"Provider ID."`
	Body struct {
		DisplayName  *string `json:"display_name,omitempty" minLength:"1" maxLength:"64" doc:"Text for the sign-in button, 1 to 64 characters."`
		DiscoveryURL *string `json:"discovery_url,omitempty" minLength:"1" maxLength:"512" doc:"OIDC discovery URL."`
		ClientID     *string `json:"client_id,omitempty" minLength:"1" maxLength:"256" doc:"OAuth client ID."`
		ClientSecret *string `json:"client_secret,omitempty" minLength:"1" maxLength:"1024" doc:"New OAuth client secret. Omit to keep the stored one; an empty string is rejected."`
		ProviderParams
	}
}

type discoverIn struct {
	Body struct {
		DiscoveryURL string `json:"discovery_url" minLength:"1" maxLength:"512" doc:"OIDC discovery URL: the issuer URL or the full .well-known/openid-configuration URL."`
	}
}

func errNoProvider() error { return apiErr(http.StatusNotFound, "not_found", "no such provider") }

func (d Deps) registerOIDCProviders(api huma.API) {
	register(api, route{Method: http.MethodGet, Path: "/api/admin/oidc-providers", ID: "listOIDCProviders", Tag: "oidc",
		Summary:     "List OIDC providers",
		Description: "Returns every configured OIDC provider ordered by display name, enabled or not. Client secrets are never returned.",
		Scope:       auth.ScopeAdmin},
		func(ctx context.Context, _ *noIn) (*providersBody, error) {
			ps, err := d.Store.ListOIDCProviders(ctx)
			if err != nil {
				return nil, err
			}
			r := httpRequest(ctx)
			out := &providersBody{Body: make([]ProviderOut, 0, len(ps))}
			for _, p := range ps {
				out.Body = append(out.Body, d.providerOut(ctx, r, p))
			}
			return out, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/admin/oidc-providers", ID: "createOIDCProvider", Tag: "oidc",
		Summary: "Add an OIDC provider",
		Description: "Adds a provider such as Authentik, Keycloak or any other OpenID Connect server. The slug becomes part of " +
			"the login and callback URLs and cannot be changed. Register the returned `redirect_uri` at the provider. " +
			"A duplicate slug returns 409 `slug_taken`. The client secret is sealed at rest and never returned.",
		Scope: auth.ScopeAdmin, Status: http.StatusCreated, Errors: []int{http.StatusConflict}},
		func(ctx context.Context, in *createProviderIn) (*providerBody, error) {
			b := in.Body
			if err := validDiscoveryURL(b.DiscoveryURL); err != nil {
				return nil, err
			}
			name := strings.TrimSpace(b.DisplayName)
			if name == "" {
				return nil, apiErr(http.StatusBadRequest, "invalid_request", "display_name: must not be blank")
			}
			p := store.OIDCProvider{Slug: b.Slug, DisplayName: name, IssuerURL: b.DiscoveryURL, ClientID: b.ClientID,
				ClientSecretEnc: d.Box.SealFor(b.ClientSecret, providerSecretAAD(b.Slug)),
				Scopes:          defaultOIDCScopes, GroupsClaim: defaultOIDCGroupsClaim,
				AutoCreateUsers: true, Enabled: true}
			b.ProviderParams.apply(&p)
			p, err := d.Store.CreateOIDCProvider(ctx, p)
			if errors.Is(err, store.ErrNameConflict) {
				return nil, apiErr(http.StatusConflict, "slug_taken", "a provider with this slug already exists")
			}
			if err != nil {
				return nil, err
			}
			return &providerBody{Body: d.providerOut(ctx, httpRequest(ctx), p)}, nil
		})

	register(api, route{Method: http.MethodGet, Path: "/api/admin/oidc-providers/{id}", ID: "getOIDCProvider", Tag: "oidc",
		Summary:     "Get an OIDC provider",
		Description: "Returns one provider. The client secret is never returned; `has_client_secret` says whether one is stored.",
		Scope:       auth.ScopeAdmin},
		func(ctx context.Context, in *providerIDIn) (*providerBody, error) {
			p, err := d.Store.OIDCProvider(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoProvider()
			}
			if err != nil {
				return nil, err
			}
			return &providerBody{Body: d.providerOut(ctx, httpRequest(ctx), p)}, nil
		})

	register(api, route{Method: http.MethodPatch, Path: "/api/admin/oidc-providers/{id}", ID: "updateOIDCProvider", Tag: "oidc",
		Summary: "Update an OIDC provider",
		Description: "Changes the fields present in the body. The slug cannot be changed. `client_secret` replaces the stored " +
			"secret when present; an empty string is rejected with 400, so omit it to keep the current one. A change takes " +
			"effect on the next sign-in; sessions already open are unaffected.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *patchProviderIn) (*providerBody, error) {
			p, err := d.Store.OIDCProvider(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoProvider()
			}
			if err != nil {
				return nil, err
			}
			b := in.Body
			if b.DisplayName != nil {
				if p.DisplayName = strings.TrimSpace(*b.DisplayName); p.DisplayName == "" {
					return nil, apiErr(http.StatusBadRequest, "invalid_request", "display_name: must not be blank")
				}
			}
			if b.DiscoveryURL != nil {
				if err := validDiscoveryURL(*b.DiscoveryURL); err != nil {
					return nil, err
				}
				p.IssuerURL = *b.DiscoveryURL
			}
			set(&p.ClientID, b.ClientID)
			if b.ClientSecret != nil {
				p.ClientSecretEnc = d.Box.SealFor(*b.ClientSecret, providerSecretAAD(p.Slug))
			}
			b.ProviderParams.apply(&p)
			p, err = d.Store.UpdateOIDCProvider(ctx, p)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoProvider()
			}
			if err != nil {
				return nil, err
			}
			d.OIDC.Forget(p.Slug)
			return &providerBody{Body: d.providerOut(ctx, httpRequest(ctx), p)}, nil
		})

	register(api, route{Method: http.MethodDelete, Path: "/api/admin/oidc-providers/{id}", ID: "deleteOIDCProvider", Tag: "oidc",
		Summary: "Delete an OIDC provider",
		Description: "Removes the provider. Users who signed in through it keep their accounts and sessions, but can no " +
			"longer sign in with it.",
		Scope: auth.ScopeAdmin, Status: http.StatusNoContent},
		func(ctx context.Context, in *providerIDIn) (*noContent, error) {
			p, err := d.Store.OIDCProvider(ctx, in.ID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, errNoProvider()
			}
			if err != nil {
				return nil, err
			}
			if err := d.Store.DeleteOIDCProvider(ctx, in.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
			d.OIDC.Forget(p.Slug)
			return &noContent{}, nil
		})

	register(api, route{Method: http.MethodPost, Path: "/api/admin/oidc-providers/discover", ID: "discoverOIDCProvider", Tag: "oidc",
		Summary: "Test an OIDC discovery URL",
		Description: "Fetches the discovery document at the URL (the `.well-known/openid-configuration` suffix is added when " +
			"missing), timing out after 5 seconds, and reports the issuer it publishes. The result is always HTTP 200; " +
			"check `ok`.",
		Scope: auth.ScopeAdmin},
		func(ctx context.Context, in *discoverIn) (*testOut, error) {
			start := time.Now()
			ctx, cancel := context.WithTimeout(ctx, connTestTimeout)
			defer cancel()
			iss, err := oidcauth.ResolveIssuer(ctx, d.HTTPClient, in.Body.DiscoveryURL)
			if err != nil {
				return result(start, false, "%s", err.Error()), nil
			}
			return result(start, true, "issuer %s", iss), nil
		})
}
