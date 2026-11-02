package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/metril/wattson/internal/oidcauth"
	"github.com/metril/wattson/internal/store"
)

const (
	oidcCookie     = "wattson_oidc"
	oidcCookiePath = "/api/auth/oidc/"
	oidcCookieAge  = 300 // seconds; the signed value itself expires after 5 minutes
	oidcTimeout    = 15 * time.Second
	oidcStateLabel = "wattson oidc state v1"
)

var (
	errNotProvisioned = errors.New("no local user for this identity")
	errInactive       = errors.New("user is disabled")
	errLinkRefused    = errors.New("link by email refused: the local user is an admin")
)

func init() {
	registerSpec(func() []opSpec {
		return []opSpec{
			{Method: http.MethodGet, Path: "/api/auth/oidc/{slug}/login", ID: "oidcLogin", Tag: "oidc",
				Summary: "Start an OIDC sign-in",
				Description: "Redirects (302) the browser to the provider's authorization endpoint using the authorization code " +
					"flow with S256 PKCE and a nonce, and sets the short-lived `wattson_oidc` cookie that binds the attempt to " +
					"this provider. `return_to` must be a same-origin path (it must start with a single `/`); anything else " +
					"is replaced by `/`. An unknown or disabled provider redirects to `/login?error=unknown_provider`, and " +
					"one whose discovery fails to `/login?error=provider_unavailable`. No authentication required.",
				Params: []p{
					{Name: "slug", Doc: "Provider slug.", Type: "string"},
					{Name: "return_to", Doc: "Same-origin path to open after signing in, for example `/settings?tab=bike`. Default `/`.", Type: "string", Query: true},
				},
				Status: http.StatusFound, NoStdErrors: true},
			{Method: http.MethodGet, Path: "/api/auth/oidc/{slug}/callback", ID: "oidcCallback", Tag: "oidc",
				Summary: "Finish an OIDC sign-in",
				Description: "The redirect URI registered at the provider. Checks the provider's `error` parameter, the signed " +
					"state cookie (same provider, same `state`), exchanges the code with the PKCE verifier, verifies the ID " +
					"token and its nonce, applies the allowed groups and emails, finds, links or creates the local user, " +
					"syncs admin status from the admin group, and starts a session. Always answers 302: to the `return_to` " +
					"path on success, otherwise to `/login?error=<code>` where code is one of `unknown_provider`, " +
					"`provider_unavailable`, `provider_error`, `state`, `exchange_failed`, `forbidden`, `not_provisioned` " +
					"or `inactive`. No authentication required.",
				Params: []p{
					{Name: "slug", Doc: "Provider slug.", Type: "string"},
					{Name: "code", Doc: "Authorization code from the provider.", Type: "string", Query: true},
					{Name: "state", Doc: "State echoed by the provider; must match the state cookie.", Type: "string", Query: true},
					{Name: "error", Doc: "Error code from the provider, when it refused the sign-in.", Type: "string", Query: true},
				},
				Status: http.StatusFound, NoStdErrors: true},
		}
	})
}

// registerOIDCFlow mounts the browser redirects of the OIDC login flow.
func (d Deps) registerOIDCFlow(r chi.Router) {
	r.Get("/api/auth/oidc/{slug}/login", d.handleOIDCLogin)
	r.Get("/api/auth/oidc/{slug}/callback", d.handleOIDCCallback)
}

// newStateCodec derives the state signing key from the sealing box.
func newStateCodec(d Deps) *oidcauth.StateCodec {
	return oidcauth.NewStateCodec(d.Box.Derive(oidcStateLabel))
}

func randomToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("api: entropy source failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// sanitizeReturnTo restricts a post-login target to a same-origin path. It
// must start with a single "/", carry no scheme, host or userinfo, contain
// no backslash (raw or percent-decoded, which browsers read as "/"), no
// empty-leading "//" after decoding, and no "." or ".." segment (which
// path cleaning would collapse into something else). Anything else becomes
// "/". The result is written to Location verbatim, see redirectTo.
func sanitizeReturnTo(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(s, "/") {
		return "/"
	}
	dec, err := url.PathUnescape(s)
	if err != nil || strings.Contains(s, `\`) || strings.Contains(dec, `\`) ||
		strings.HasPrefix(s, "//") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return "/"
		}
	}
	return s
}

// redirectTo answers 302 with Location set verbatim. http.Redirect would
// run path.Clean over a relative target, which can turn a sanitised value
// into a different one.
func redirectTo(w http.ResponseWriter, loc string) {
	w.Header().Set("Location", loc)
	w.WriteHeader(http.StatusFound)
}

func loginError(w http.ResponseWriter, r *http.Request, code string) {
	redirectTo(w, "/login?error="+code)
}

func (d Deps) oidcCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	pub := ""
	var trusted []netip.Prefix
	if s, err := d.Settings.Get(r.Context()); err == nil {
		pub, trusted = s.General.PublicURL, trustedProxies(s)
	}
	return &http.Cookie{Name: oidcCookie, Value: value, Path: oidcCookiePath, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureCookie(r, pub, trusted)}
}

// oidcFail logs the reason, clears the state cookie and redirects to the
// login page with the public error code. args are extra slog pairs and must
// never carry tokens or secrets.
func (d Deps) oidcFail(w http.ResponseWriter, r *http.Request, code, slug string, err error, args ...any) {
	a := []any{"reason", code, "slug", slug}
	if err != nil {
		a = append(a, "err", err)
	}
	d.Logger.Warn("oidc login failed", append(a, args...)...)
	http.SetCookie(w, d.oidcCookie(r, "", -1))
	loginError(w, r, code)
}

// loadProvider returns the enabled provider for slug as a discovered
// oidcauth.Provider. On failure code is the public error code.
func (d Deps) loadProvider(ctx context.Context, slug string) (p *oidcauth.Provider, row store.OIDCProvider, code string, err error) {
	row, err = d.Store.OIDCProviderBySlug(ctx, slug)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !row.Enabled) {
		return nil, row, "unknown_provider", errors.New("no such enabled provider")
	}
	if err != nil {
		return nil, row, "provider_unavailable", err
	}
	secret, err := d.Box.OpenFor(row.ClientSecretEnc, providerSecretAAD(row.Slug))
	if err != nil {
		return nil, row, "provider_unavailable", fmt.Errorf("open client secret: %w", err)
	}
	issuer, err := oidcauth.ResolveIssuer(ctx, d.HTTPClient, row.IssuerURL)
	if err != nil {
		return nil, row, "provider_unavailable", err
	}
	cfg := oidcauth.Config{Slug: row.Slug, Issuer: issuer, ClientID: row.ClientID, ClientSecret: secret,
		Scopes: strings.Fields(row.Scopes), GroupsClaim: row.GroupsClaim, AdminGroup: row.AdminGroup,
		AllowedGroups: row.AllowedGroups, AllowedEmails: row.AllowedEmails}
	p, err = d.OIDC.Get(ctx, cfg, row.UpdatedAt.Format(store.TimeFormat))
	if err != nil {
		return nil, row, "provider_unavailable", err
	}
	return p, row, "", nil
}

func (d Deps) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ctx, cancel := context.WithTimeout(r.Context(), oidcTimeout)
	defer cancel()
	p, _, code, err := d.loadProvider(ctx, slug)
	if err != nil {
		d.oidcFail(w, r, code, slug, err)
		return
	}
	st := oidcauth.State{Slug: slug, State: randomToken(), Nonce: randomToken(), Verifier: oauth2.GenerateVerifier(),
		ReturnTo: sanitizeReturnTo(r.URL.Query().Get("return_to"))}
	http.SetCookie(w, d.oidcCookie(r, d.oidcState.Encode(st, d.Now()), oidcCookieAge))
	http.Redirect(w, r, p.AuthCodeURL(st.State, st.Nonce, st.Verifier, d.redirectURI(ctx, r, slug)), http.StatusFound)
}

func (d Deps) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	q := r.URL.Query()
	ctx, cancel := context.WithTimeout(r.Context(), oidcTimeout)
	defer cancel()

	if e := q.Get("error"); e != "" {
		d.oidcFail(w, r, "provider_error", slug, nil, "idp_error", truncate(e, 100),
			"idp_description", truncate(q.Get("error_description"), 200))
		return
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil || c.Value == "" {
		d.oidcFail(w, r, "state", slug, errors.New("state cookie missing"))
		return
	}
	st, err := d.oidcState.Decode(c.Value, d.Now())
	if err != nil {
		d.oidcFail(w, r, "state", slug, err)
		return
	}
	if st.Slug != slug {
		d.oidcFail(w, r, "state", slug, errors.New("state cookie belongs to another provider"), "cookie_slug", st.Slug)
		return
	}
	if got := q.Get("state"); st.State == "" || subtle.ConstantTimeCompare([]byte(got), []byte(st.State)) != 1 {
		d.oidcFail(w, r, "state", slug, errors.New("state parameter mismatch"))
		return
	}
	code := q.Get("code")
	if code == "" {
		d.oidcFail(w, r, "provider_error", slug, errors.New("no code in callback"))
		return
	}
	p, row, failCode, err := d.loadProvider(ctx, slug)
	if err != nil {
		d.oidcFail(w, r, failCode, slug, err)
		return
	}
	cl, err := p.Exchange(ctx, code, st.Verifier, st.Nonce, d.redirectURI(ctx, r, slug))
	if err != nil {
		d.oidcFail(w, r, "exchange_failed", slug, err)
		return
	}
	cfg := p.Config()
	summary := []any{"sub", cl.Subject, "email", cl.Email, "email_verified", cl.EmailVerified, "groups", cl.Groups}
	if err := cfg.Authorize(cl); err != nil {
		d.oidcFail(w, r, "forbidden", slug, err, append(summary, "groups_claim", cfg.GroupsClaim,
			"allowed_groups", cfg.AllowedGroups, "allowed_emails", cfg.AllowedEmails, "userinfo_err", cl.UserInfoErr)...)
		return
	}
	u, err := d.resolveUser(ctx, row, cl)
	switch {
	case errors.Is(err, errNotProvisioned), errors.Is(err, errLinkRefused):
		d.oidcFail(w, r, "not_provisioned", slug, err, summary...)
		return
	case errors.Is(err, errInactive):
		d.oidcFail(w, r, "inactive", slug, err, summary...)
		return
	case err != nil:
		d.Logger.Error("oidc login: resolve user", "slug", slug, "err", err)
		http.SetCookie(w, d.oidcCookie(r, "", -1))
		writeEnvelope(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	u = d.syncAdmin(ctx, cfg, cl, u)

	cookie, err := d.startSession(ctx, r, u.ID)
	if err != nil {
		d.Logger.Error("oidc login: start session", "slug", slug, "err", err)
		http.SetCookie(w, d.oidcCookie(r, "", -1))
		writeEnvelope(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	http.SetCookie(w, d.oidcCookie(r, "", -1))
	http.SetCookie(w, &cookie)
	redirectTo(w, sanitizeReturnTo(st.ReturnTo))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// syncAdmin applies the IdP's admin-group decision to u. A refusal from the
// store (bootstrap admin, last admin) keeps the current flag.
func (d Deps) syncAdmin(ctx context.Context, cfg oidcauth.Config, cl oidcauth.Claims, u store.User) store.User {
	admin, managed := cfg.AdminDecision(cl)
	if !managed || admin == u.IsAdmin {
		return u
	}
	if cl.UserInfoErr != nil {
		// The groups may be missing only because userinfo failed; never
		// change admin status on an IdP glitch.
		d.Logger.Warn("oidc admin sync skipped", "user", u.Username, "want_admin", admin,
			"reason", "groups unavailable: "+cl.UserInfoErr.Error())
		return u
	}
	nu, err := d.Store.UpdateUserFlags(ctx, u.ID, &admin, nil)
	switch {
	case err == nil:
		return nu
	case errors.Is(err, store.ErrBootstrap), errors.Is(err, store.ErrLastAdmin):
		d.Logger.Warn("oidc admin sync skipped", "user", u.Username, "want_admin", admin, "reason", err.Error())
	default:
		d.Logger.Error("oidc admin sync failed", "user", u.Username, "err", err)
	}
	return u
}

func active(u store.User) (store.User, error) {
	if u.Disabled {
		return store.User{}, errInactive
	}
	return u, nil
}

// resolveUser maps a verified identity to a local user: the (iss, sub) link,
// else a link to the local user with the same verified email (when enabled
// and exactly one matches), else a new user (when enabled). errNotProvisioned
// and errInactive are the refusals; anything else is an internal failure.
func (d Deps) resolveUser(ctx context.Context, prov store.OIDCProvider, cl oidcauth.Claims) (store.User, error) {
	byIdentity := func() (store.User, error) { return d.Store.UserByIssSub(ctx, cl.Issuer, cl.Subject) }
	u, err := byIdentity()
	switch {
	case err == nil:
		return active(u)
	case !errors.Is(err, store.ErrNotFound):
		return store.User{}, err
	}

	email := strings.TrimSpace(cl.Email)
	if prov.LinkByEmail && cl.EmailVerified && email != "" {
		lu, err := d.Store.UserByEmail(ctx, email)
		switch {
		case err == nil:
			if lu.Disabled {
				return store.User{}, errInactive
			}
			// An IdP-asserted email must never hand an admin account to
			// whoever controls that address at the IdP.
			if lu.IsAdmin {
				return store.User{}, errLinkRefused
			}
			err := d.Store.LinkOIDC(ctx, lu.ID, cl.Issuer, cl.Subject)
			if err == nil {
				lu.Iss, lu.Sub = cl.Issuer, cl.Subject
				return lu, nil
			}
			if errors.Is(err, store.ErrNameConflict) { // the identity got linked concurrently
				if u, e := byIdentity(); e == nil {
					return active(u)
				}
			}
			return store.User{}, err
		case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrAmbiguous):
			// no unambiguous local user to link to
		default:
			return store.User{}, err
		}
	}

	if !prov.AutoCreateUsers {
		return store.User{}, errNotProvisioned
	}
	return d.createOIDCUser(ctx, cl, email, byIdentity)
}

func (d Deps) createOIDCUser(ctx context.Context, cl oidcauth.Claims, email string, byIdentity func() (store.User, error)) (store.User, error) {
	stored := ""
	if cl.EmailVerified && email != "" {
		used, err := d.Store.EmailInUse(ctx, email)
		if err != nil {
			return store.User{}, err
		}
		if !used {
			stored = email
		}
	}
	base := usernameBase(cl)
	display := strings.TrimSpace(cl.Name)
	for i := 1; i <= 100; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		dn := display
		if dn == "" {
			dn = name
		}
		u, err := d.Store.CreateUser(ctx, store.User{Username: name, Email: stored, DisplayName: dn,
			Iss: cl.Issuer, Sub: cl.Subject})
		if err == nil {
			return u, nil
		}
		if !errors.Is(err, store.ErrNameConflict) {
			return store.User{}, err
		}
		// Either the username is taken or this identity was created concurrently.
		if ex, e := byIdentity(); e == nil {
			return active(ex)
		}
	}
	return store.User{}, errors.New("no free username")
}

// usernameBase picks the first non-empty of preferred_username, the email
// local part and the subject, with whitespace runs turned into hyphens.
func usernameBase(cl oidcauth.Claims) string {
	local, _, _ := strings.Cut(cl.Email, "@")
	for _, c := range []string{cl.PreferredUsername, local, cl.Subject} {
		if s := strings.Join(strings.Fields(c), "-"); s != "" {
			if utf8.RuneCountInString(s) > 64 {
				s = string([]rune(s)[:64])
			}
			return s
		}
	}
	return "user"
}
