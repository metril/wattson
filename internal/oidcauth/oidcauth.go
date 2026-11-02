// Package oidcauth implements OpenID Connect login: provider discovery, the
// authorization-code flow with S256 PKCE and a nonce, ID token verification
// and claim based authorisation. It works with any compliant provider
// (Authentik, Keycloak, Google, ...).
package oidcauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var (
	// ErrForbidden is returned by Config.Authorize when the claims fail the
	// allowed-groups or allowed-emails rule.
	ErrForbidden = errors.New("oidcauth: forbidden")
	// ErrNonce is returned by Provider.Exchange when the ID token's nonce
	// is missing or differs from the one sent in the authorization request.
	ErrNonce = errors.New("oidcauth: id token nonce mismatch")
)

// Config configures one provider and the authorisation rules applied to the
// claims it returns.
type Config struct {
	Slug          string
	Issuer        string // exactly as the discovery document publishes it, see ResolveIssuer
	ClientID      string
	ClientSecret  string
	Scopes        []string // added to the always-requested openid, profile and email
	GroupsClaim   string
	AdminGroup    string
	AllowedGroups []string
	AllowedEmails []string
}

// Claims is the identity extracted from a verified ID token.
type Claims struct {
	Issuer            string
	Subject           string
	Email             string
	Name              string
	PreferredUsername string
	// EmailVerified is true only when the provider asserted
	// email_verified=true for Email. A missing claim is false.
	EmailVerified bool
	Groups        []string

	// UserInfoErr is set when the groups claim was absent from the ID token
	// and the fallback userinfo request failed. Login proceeds on the ID
	// token alone; callers log it so a resulting forbidden is explainable.
	UserInfoErr error
}

// Provider is a discovered OIDC provider ready to run the authorization
// code flow.
type Provider struct {
	cfg      Config
	client   *http.Client
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauthCfg oauth2.Config
}

// clientContext makes client the HTTP client for go-oidc and oauth2 calls.
func clientContext(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		return ctx
	}
	ctx = oidc.ClientContext(ctx, client)
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

// New discovers the provider at cfg.Issuer. client, when non-nil, is used
// for every HTTP call the provider makes (discovery, JWKS, token, userinfo).
func New(ctx context.Context, cfg Config, client *http.Client) (*Provider, error) {
	ctx = clientContext(ctx, client)
	p, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidcauth: discover %q: %w", cfg.Issuer, err)
	}
	return &Provider{
		cfg:      cfg,
		client:   client,
		provider: p,
		verifier: p.VerifierContext(ctx, &oidc.Config{ClientID: cfg.ClientID}),
		oauthCfg: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     p.Endpoint(),
			Scopes:       dedupScopes(cfg.Scopes),
		},
	}, nil
}

// dedupScopes returns "openid profile email" plus extra, each once.
func dedupScopes(extra []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append([]string{"openid", "profile", "email"}, extra...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Config returns the configuration the Provider was built with.
func (p *Provider) Config() Config { return p.cfg }

// AuthCodeURL returns the URL to send the browser to: an authorization-code
// request with the S256 challenge of verifier and the given nonce.
func (p *Provider) AuthCodeURL(state, nonce, verifier, redirectURI string) string {
	cfg := p.oauthCfg
	cfg.RedirectURL = redirectURI
	return cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
}

// Exchange trades an authorization code for tokens, verifies the ID token
// (signature, issuer, audience, expiry and nonce) and decodes its claims.
// When the configured groups claim is absent from the ID token and the
// provider advertises a userinfo endpoint, claims are completed from
// userinfo: Authentik (unless "include claims in ID token" is on), Okta and
// others only emit groups there. A failed userinfo request is recorded in
// Claims.UserInfoErr instead of failing the login; a userinfo document for a
// different subject is an error.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce, redirectURI string) (Claims, error) {
	ctx = clientContext(ctx, p.client)
	cfg := p.oauthCfg
	cfg.RedirectURL = redirectURI

	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Claims{}, fmt.Errorf("oidcauth: exchange: %w", err)
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Claims{}, errors.New("oidcauth: exchange: no id_token in token response")
	}
	idTok, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return Claims{}, fmt.Errorf("oidcauth: verify id_token: %w", err)
	}
	if nonce == "" || subtle.ConstantTimeCompare([]byte(idTok.Nonce), []byte(nonce)) != 1 {
		return Claims{}, ErrNonce
	}

	var raw map[string]any
	if err := idTok.Claims(&raw); err != nil {
		return Claims{}, fmt.Errorf("oidcauth: decode claims: %w", err)
	}
	claims := Claims{Issuer: idTok.Issuer, Subject: idTok.Subject}
	claims.fill(raw)

	if _, has := raw[p.cfg.GroupsClaim]; p.cfg.GroupsClaim != "" && !has && p.userInfoEndpoint() != "" {
		info, err := p.userInfo(ctx, tok)
		switch {
		case err != nil:
			claims.UserInfoErr = err
		default:
			if sub, _ := info["sub"].(string); sub != idTok.Subject {
				return Claims{}, fmt.Errorf("oidcauth: userinfo: sub %q does not match id_token sub %q", sub, idTok.Subject)
			}
			claims.fill(info)
			raw = info
		}
	}
	claims.Groups = extractGroups(raw, p.cfg.GroupsClaim)
	return claims, nil
}

// fill copies the profile claims from raw into c. Values c already has win
// (ID token over userinfo). email_verified travels with the email it
// describes: it is taken from raw only when raw's email is the one c holds.
func (c *Claims) fill(raw map[string]any) {
	set := func(dst *string, key string) {
		if *dst == "" {
			if v, ok := raw[key].(string); ok {
				*dst = v
			}
		}
	}
	email, _ := raw["email"].(string)
	if c.Email == "" && email != "" {
		c.Email = email
	}
	if email != "" && strings.EqualFold(email, c.Email) && !c.EmailVerified {
		c.EmailVerified = asBool(raw["email_verified"])
	}
	set(&c.Name, "name")
	set(&c.PreferredUsername, "preferred_username")
}

// asBool reads a JSON boolean, also accepting the string "true" that some
// providers (for example AWS Cognito) send.
func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	}
	return false
}

func (p *Provider) userInfoEndpoint() string {
	var meta struct {
		UserInfoEndpoint string `json:"userinfo_endpoint"`
	}
	if p.provider == nil || p.provider.Claims(&meta) != nil {
		return ""
	}
	return meta.UserInfoEndpoint
}

func (p *Provider) userInfo(ctx context.Context, tok *oauth2.Token) (map[string]any, error) {
	ui, err := p.provider.UserInfo(ctx, oauth2.StaticTokenSource(tok))
	if err != nil {
		return nil, fmt.Errorf("oidcauth: userinfo: %w", err)
	}
	var info map[string]any
	if err := ui.Claims(&info); err != nil {
		return nil, fmt.Errorf("oidcauth: userinfo: decode claims: %w", err)
	}
	return info, nil
}

// extractGroups reads claim from raw, accepting a JSON array (non-string
// entries are dropped) or a space- or comma-separated string. A missing
// claim yields nil.
func extractGroups(raw map[string]any, claim string) []string {
	if claim == "" {
		return nil
	}
	switch t := raw[claim].(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' })
	}
	return nil
}

// Authorize applies the allowed-groups and allowed-emails rules (each is
// skipped when empty; comparisons ignore case) and returns ErrForbidden when
// one fails. An allowed-emails match also needs EmailVerified, since that is
// the one rule that trusts the address itself; group rules trust the IdP's
// group assignment.
func (c Config) Authorize(cl Claims) error {
	if len(c.AllowedGroups) > 0 && !intersects(cl.Groups, c.AllowedGroups) {
		return ErrForbidden
	}
	if len(c.AllowedEmails) > 0 && (!cl.EmailVerified || !containsFold(c.AllowedEmails, cl.Email)) {
		return ErrForbidden
	}
	return nil
}

// AdminDecision reports whether cl is in the admin group. managed is false
// when no AdminGroup is configured; the IdP then has no say over admin
// status and admin is false.
func (c Config) AdminDecision(cl Claims) (admin, managed bool) {
	if c.AdminGroup == "" {
		return false, false
	}
	return containsFold(cl.Groups, c.AdminGroup), true
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if containsFold(b, x) {
			return true
		}
	}
	return false
}

func containsFold(list []string, target string) bool {
	for _, s := range list {
		if strings.EqualFold(s, target) {
			return true
		}
	}
	return false
}
