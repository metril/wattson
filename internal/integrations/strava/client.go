// Package strava talks to the Strava API: OAuth with rotating refresh
// tokens, FIT upload and upload status. It never logs or returns tokens or
// client secrets in errors.
package strava

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Scope is the one scope element Wattson requests. Strava takes a comma
// list, so it is a single oauth2 scope (oauth2 would join several with
// spaces).
const Scope = "read,activity:write"

const (
	defaultTimeout = 30 * time.Second
	maxResponse    = 1 << 20 // bytes read from any Strava response
)

// Endpoints are the Strava URLs. Production values are Prod; tests inject
// a fake server's. There is deliberately no other way to change them.
type Endpoints struct{ AuthURL, TokenURL, DeauthURL, APIBase string }

// Prod are the real Strava endpoints.
var Prod = Endpoints{"https://www.strava.com/oauth/authorize", "https://www.strava.com/oauth/token",
	"https://www.strava.com/oauth/deauthorize", "https://www.strava.com/api/v3"}

// App is a Strava API application's credentials.
type App struct{ ClientID, ClientSecret string }

// Athlete is the Strava account behind a grant.
type Athlete struct {
	ID   int64
	Name string // "firstname lastname"
}

// Grant is the result of exchanging an authorisation code.
type Grant struct {
	Token   *oauth2.Token
	Athlete Athlete
	Scope   string // scope reported by the token response, if any
}

// Upload is Strava's view of an uploaded file. ActivityID is 0 until
// processing finishes; Error is set when processing failed.
type Upload struct {
	ID         int64
	ExternalID string
	Status     string
	Error      string
	ActivityID int64
}

// UploadReq is one FIT file to upload.
type UploadReq struct {
	FIT                                     []byte
	Filename, Name, Description, ExternalID string
	Trainer                                 bool
}

var (
	// ErrRateLimited reports a 429 from Strava.
	ErrRateLimited = errors.New("strava: rate limited")
	// ErrUnauthorized reports a 401 from Strava, or a refresh token Strava
	// rejects: the link must be re-authorised.
	ErrUnauthorized = errors.New("strava: unauthorized")
)

// Client is a Strava API client over one set of endpoints.
type Client struct {
	ep Endpoints
	hc *http.Client
}

// NewClient returns a client using hc (nil: a fresh client). The client is
// copied: a zero timeout becomes 30 s and every response body is capped at
// 1 MiB.
func NewClient(ep Endpoints, hc *http.Client) *Client {
	c := http.Client{}
	if hc != nil {
		c = *hc
	}
	if c.Timeout == 0 {
		c.Timeout = defaultTimeout
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = capTransport{base}
	return &Client{ep: ep, hc: &c}
}

// capTransport limits how much of a response body can be read.
type capTransport struct{ base http.RoundTripper }

func (t capTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = &capBody{rc: resp.Body, left: maxResponse}
	return resp, nil
}

type capBody struct {
	rc   io.ReadCloser
	left int64
}

var errTooLarge = errors.New("strava: response too large")

func (b *capBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// Distinguish a body that ends exactly at the cap from one beyond it.
		var one [1]byte
		if n, _ := b.rc.Read(one[:]); n > 0 {
			return 0, errTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *capBody) Close() error { return b.rc.Close() }

func (c *Client) config(app App, redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     app.ClientID,
		ClientSecret: app.ClientSecret,
		RedirectURL:  redirectURI,
		Scopes:       []string{Scope},
		Endpoint:     oauth2.Endpoint{AuthURL: c.ep.AuthURL, TokenURL: c.ep.TokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
}

// httpCtx makes oauth2 use the capped client and outlive request cancellation
// (a token source is kept past the request that built it; hc.Timeout bounds
// every call).
func (c *Client) httpCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c.hc)
}

// AuthCodeURL is the Strava authorize URL. There is no PKCE: Strava does not
// support it, so state carries the CSRF binding.
func (c *Client) AuthCodeURL(app App, redirectURI, state string) string {
	return c.config(app, redirectURI).AuthCodeURL(state, oauth2.SetAuthURLParam("approval_prompt", "auto"))
}

// Exchange trades an authorisation code for tokens and the athlete.
func (c *Client) Exchange(ctx context.Context, app App, redirectURI, code string) (Grant, error) {
	tok, err := c.config(app, redirectURI).Exchange(c.httpCtx(ctx), code)
	if err != nil {
		return Grant{}, fmt.Errorf("strava: exchange: %w", scrub(err))
	}
	g := Grant{Token: tok}
	if m, ok := tok.Extra("athlete").(map[string]any); ok {
		if id, ok := m["id"].(float64); ok {
			g.Athlete.ID = int64(id)
		}
		first, _ := m["firstname"].(string)
		last, _ := m["lastname"].(string)
		g.Athlete.Name = strings.TrimSpace(first + " " + last)
	}
	g.Scope, _ = tok.Extra("scope").(string)
	if g.Athlete.ID == 0 {
		return Grant{}, errors.New("strava: exchange: token response has no athlete")
	}
	return g, nil
}

// scrub reduces an oauth2 error to its status code: the body may echo
// credentials.
func scrub(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return fmt.Errorf("token endpoint answered %d", re.Response.StatusCode)
	}
	return err
}

// TokenSource returns a source that serves tok until it expires, then
// refreshes it. Strava rotates the refresh token on every refresh, so
// onRefresh runs (and its error is returned, with the token not served)
// whenever the access token changes: persist the new tokens there before
// they are used. A refresh Strava refuses (400 or 401) wraps ErrUnauthorized.
func (c *Client) TokenSource(ctx context.Context, app App, tok *oauth2.Token, onRefresh func(*oauth2.Token) error) oauth2.TokenSource {
	base := c.config(app, "").TokenSource(c.httpCtx(context.WithoutCancel(ctx)), tok)
	return oauth2.ReuseTokenSource(tok, &notifySource{base: base, last: tok.AccessToken, on: onRefresh})
}

type notifySource struct {
	base oauth2.TokenSource
	last string // access token already handed to on
	on   func(*oauth2.Token) error
}

func (s *notifySource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.Response.StatusCode == http.StatusBadRequest || re.Response.StatusCode == http.StatusUnauthorized) {
			return nil, fmt.Errorf("strava: refresh refused (%d): %w", re.Response.StatusCode, ErrUnauthorized)
		}
		return nil, fmt.Errorf("strava: refresh: %w", scrub(err))
	}
	if t.AccessToken != s.last {
		if s.on != nil {
			if err := s.on(t); err != nil {
				return nil, err
			}
		}
		s.last = t.AccessToken
	}
	return t, nil
}

// Deauthorize revokes accessToken at Strava.
func (c *Client) Deauthorize(ctx context.Context, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ep.DeauthURL,
		strings.NewReader(url.Values{"access_token": {accessToken}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("strava: deauthorize: %w", urlErr(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("strava: deauthorize: status %d", resp.StatusCode)
	}
	return nil
}

// urlErr drops the request URL from a transport error (it can carry query
// secrets in other call shapes; kept uniform).
func urlErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

type uploadJSON struct {
	ID         int64   `json:"id"`
	ExternalID string  `json:"external_id"`
	Status     string  `json:"status"`
	Error      *string `json:"error"`
	ActivityID *int64  `json:"activity_id"`
}

func (u uploadJSON) upload() Upload {
	o := Upload{ID: u.ID, ExternalID: u.ExternalID, Status: u.Status}
	if u.Error != nil {
		o.Error = *u.Error
	}
	if u.ActivityID != nil {
		o.ActivityID = *u.ActivityID
	}
	return o
}

// Upload sends a FIT file. It returns right after Strava accepts it; poll
// UploadStatus until ActivityID or Error is set.
func (c *Client) Upload(ctx context.Context, ts oauth2.TokenSource, r UploadReq) (Upload, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fields := [][2]string{{"data_type", "fit"}, {"name", r.Name}, {"description", r.Description}, {"external_id", r.ExternalID}}
	if r.Trainer {
		fields = append(fields, [2]string{"trainer", "1"})
	}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return Upload{}, err
		}
	}
	name := r.Filename
	if name == "" {
		name = "ride.fit"
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	h.Set("Content-Type", "application/octet-stream")
	fw, err := mw.CreatePart(h)
	if err != nil {
		return Upload{}, err
	}
	if _, err := fw.Write(r.FIT); err != nil {
		return Upload{}, err
	}
	if err := mw.Close(); err != nil {
		return Upload{}, err
	}
	var j uploadJSON
	if err := c.call(ctx, ts, http.MethodPost, "/uploads", mw.FormDataContentType(), &body, &j); err != nil {
		return Upload{}, err
	}
	return j.upload(), nil
}

// UploadStatus reports the processing state of upload id.
func (c *Client) UploadStatus(ctx context.Context, ts oauth2.TokenSource, id int64) (Upload, error) {
	var j uploadJSON
	if err := c.call(ctx, ts, http.MethodGet, "/uploads/"+strconv.FormatInt(id, 10), "", nil, &j); err != nil {
		return Upload{}, err
	}
	return j.upload(), nil
}

// call performs an authorised API request and decodes the JSON answer.
func (c *Client) call(ctx context.Context, ts oauth2.TokenSource, method, path, contentType string, body io.Reader, out any) error {
	tok, err := ts.Token()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.ep.APIBase+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	tok.SetAuthHeader(req)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("strava: %s %s: %w", method, path, urlErr(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("strava: %s %s: read: %w", method, path, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("strava: %s %s: %w", method, path, ErrUnauthorized)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("strava: %s %s: %w", method, path, ErrRateLimited)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("strava: %s %s: status %d: %s", method, path, resp.StatusCode, apiMessage(raw))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("strava: %s %s: decode: %w", method, path, err)
	}
	return nil
}

// apiMessage extracts Strava's "message" (at most 200 bytes) from an error body.
func apiMessage(raw []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	if len(e.Message) > 200 {
		e.Message = e.Message[:200]
	}
	return e.Message
}

// HasScope reports whether the comma-separated granted list contains want.
func HasScope(granted, want string) bool {
	for _, s := range strings.Split(granted, ",") {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

var dupRE = regexp.MustCompile(`activit(?:y|ies)[ /]+(\d+)`)

// DuplicateActivityID extracts the existing activity from Strava's
// "duplicate of activity 123" upload error (or one linking /activities/123).
func DuplicateActivityID(msg string) (int64, bool) {
	if !strings.Contains(strings.ToLower(msg), "duplicate") {
		return 0, false
	}
	m := dupRE.FindStringSubmatch(msg)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	return id, err == nil && id > 0
}
