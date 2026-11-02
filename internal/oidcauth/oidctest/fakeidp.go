// Package oidctest provides an in-process fake OpenID Connect provider for
// tests: discovery, JWKS, authorize, token and userinfo endpoints backed by
// an RSA key generated per instance. It enforces S256 PKCE, binds nonces
// and client IDs to authorization codes, and issues each code only once.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const keyID = "test-key"

// IDP is a fake OIDC provider over httptest.
type IDP struct {
	// URL is the server origin (no path).
	URL string
	// Issuer is the "issuer" its discovery document reports: URL plus the
	// WithIssuerPath path, if any.
	Issuer string
	// DiscoveryURL is the full well-known discovery document URL.
	DiscoveryURL string
	// ClientID is the client ID the default test configuration uses. The
	// IDP accepts any client_id at /authorize but requires /token to present
	// the same one, and puts it in the ID token's aud.
	ClientID string
	// ClientSecret is required at /token when non-empty.
	ClientSecret string

	server *httptest.Server
	key    *rsa.PrivateKey
	path   string

	mu        sync.Mutex
	next      map[string]any
	forceNon  *string
	codes     map[string]*grant
	userinfo  map[string]any
	requests  map[string]int
	lastQuery url.Values
	seq       int
}

type grant struct {
	claims      map[string]any
	nonce       string
	challenge   string
	clientID    string
	redirectURI string
}

// Option configures NewIDP.
type Option func(*IDP)

// WithIssuerPath serves discovery under path (for example
// "/application/o/wattson/", Authentik style, with a trailing slash) and
// makes the issuer URL+path. The other endpoints stay at the server root.
func WithIssuerPath(p string) Option {
	return func(idp *IDP) { idp.path = p }
}

// NewIDP starts a fake IdP and registers its shutdown with t.Cleanup.
func NewIDP(t testing.TB, opts ...Option) *IDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("oidctest: generate key: %v", err)
	}
	idp := &IDP{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		key:          key,
		codes:        map[string]*grant{},
		requests:     map[string]int{},
	}
	for _, o := range opts {
		o(idp)
	}
	if idp.path != "" && !strings.HasPrefix(idp.path, "/") {
		idp.path = "/" + idp.path
	}

	mux := http.NewServeMux()
	wellKnown := strings.TrimRight(idp.path, "/") + "/.well-known/openid-configuration"
	mux.HandleFunc(wellKnown, idp.discovery)
	mux.HandleFunc("/jwks", idp.jwks)
	mux.HandleFunc("/authorize", idp.authorize)
	mux.HandleFunc("/token", idp.token)
	mux.HandleFunc("/userinfo", idp.userInfo)
	idp.server = httptest.NewServer(idp.count(mux))
	t.Cleanup(idp.server.Close)
	idp.URL = idp.server.URL
	idp.Issuer = idp.URL + idp.path
	idp.DiscoveryURL = idp.URL + wellKnown
	return idp
}

// Next sets the claims the next /authorize call will bind to its code.
// Standard claims (iss, aud, exp, iat, nonce) are filled in; sub defaults
// to "test-subject".
func (idp *IDP) Next(claims map[string]any) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.next = claims
}

// ForceNonce overrides the nonce echoed into the ID token issued for the
// next /authorize call.
func (idp *IDP) ForceNonce(n string) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	idp.forceNon = &n
}

// LastAuthorize returns the query of the most recent /authorize request.
func (idp *IDP) LastAuthorize() url.Values {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return idp.lastQuery
}

// SetUserInfo sets the claims /userinfo returns. Until it is called,
// /userinfo answers 404.
func (idp *IDP) SetUserInfo(claims map[string]any) {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	merged := map[string]any{"sub": "test-subject"}
	for k, v := range claims {
		merged[k] = v
	}
	idp.userinfo = merged
}

// Requests reports how many requests the fake has served for path.
func (idp *IDP) Requests(path string) int {
	idp.mu.Lock()
	defer idp.mu.Unlock()
	return idp.requests[path]
}

// Client returns the HTTP client for talking to the fake.
func (idp *IDP) Client() *http.Client { return idp.server.Client() }

// Authorize drives authURL (as built by Provider.AuthCodeURL) through
// /authorize without following the redirect and returns the code and state
// of the redirect back to the client.
func (idp *IDP) Authorize(t testing.TB, authURL string) (code, state string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(authURL)
	if err != nil {
		t.Fatalf("oidctest: authorize: %v", err)
	}
	defer resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("oidctest: authorize: status %d without Location", resp.StatusCode)
	}
	return loc.Query().Get("code"), loc.Query().Get("state")
}

func (idp *IDP) count(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.mu.Lock()
		idp.requests[r.URL.Path]++
		idp.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (idp *IDP) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                idp.Issuer,
		"authorization_endpoint":                idp.URL + "/authorize",
		"token_endpoint":                        idp.URL + "/token",
		"jwks_uri":                              idp.URL + "/jwks",
		"userinfo_endpoint":                     idp.URL + "/userinfo",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
	})
}

func (idp *IDP) jwks(w http.ResponseWriter, _ *http.Request) {
	jwk := jose.JSONWebKey{Key: &idp.key.PublicKey, KeyID: keyID, Algorithm: "RS256", Use: "sig"}
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
}

// authorize records the request, issues a code bound to the claims, nonce,
// code_challenge and client_id, and redirects back with code and state.
func (idp *IDP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	u, err := url.Parse(redirect)
	if err != nil || redirect == "" || q.Get("response_type") != "code" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}

	idp.mu.Lock()
	idp.lastQuery = q
	idp.seq++
	code := "code-" + hex.EncodeToString([]byte{byte(idp.seq >> 8), byte(idp.seq)}) + "-" + randHex()
	g := &grant{claims: idp.next, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
		clientID: q.Get("client_id"), redirectURI: redirect}
	if g.claims == nil {
		g.claims = map[string]any{}
	}
	if idp.forceNon != nil {
		g.nonce = *idp.forceNon
	}
	idp.next, idp.forceNon = nil, nil
	idp.codes[code] = g
	idp.mu.Unlock()

	rq := u.Query()
	rq.Set("code", code)
	if s := q.Get("state"); s != "" {
		rq.Set("state", s)
	}
	u.RawQuery = rq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (idp *IDP) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	clientID, secret, hasBasic := r.BasicAuth()
	if hasBasic {
		clientID, _ = url.QueryUnescape(clientID)
		secret, _ = url.QueryUnescape(secret)
	} else {
		clientID, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	if idp.ClientSecret != "" && secret != idp.ClientSecret {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostFormValue("grant_type") != "authorization_code" {
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	idp.mu.Lock()
	code := r.PostFormValue("code")
	g, ok := idp.codes[code]
	delete(idp.codes, code) // single use
	idp.mu.Unlock()
	if !ok {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	verifier := r.PostFormValue("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	if verifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	if clientID != g.clientID || r.PostFormValue("redirect_uri") != g.redirectURI {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	now := time.Now()
	merged := map[string]any{
		"iss": idp.Issuer,
		"sub": "test-subject",
		"aud": g.clientID,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	if g.nonce != "" {
		merged["nonce"] = g.nonce
	}
	for k, v := range g.claims {
		merged[k] = v
	}
	payload, err := json.Marshal(merged)
	if err != nil {
		http.Error(w, "encode claims", http.StatusInternalServerError)
		return
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		http.Error(w, "build signer", http.StatusInternalServerError)
		return
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		http.Error(w, "sign id_token", http.StatusInternalServerError)
		return
	}
	idToken, err := jws.CompactSerialize()
	if err != nil {
		http.Error(w, "serialize id_token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": "test-access-token",
		"token_type":   "bearer",
		"id_token":     idToken,
	})
}

func (idp *IDP) userInfo(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer test-access-token" {
		http.Error(w, "invalid_token", http.StatusUnauthorized)
		return
	}
	idp.mu.Lock()
	claims := idp.userinfo
	idp.mu.Unlock()
	if claims == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, claims)
}

func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
