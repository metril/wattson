package strava

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
)

const (
	stateLabel     = "wattson strava state v1"
	deauthTimeout  = 10 * time.Second
	exchangeBudget = 30 * time.Second
)

var (
	// ErrNotConfigured reports that no usable Strava app (the rider's own or
	// the shared one) has both a client ID and a secret.
	ErrNotConfigured = errors.New("strava: no app credentials configured")
	// ErrNotConnected reports a rider with no Strava authorisation.
	ErrNotConnected = errors.New("strava: rider not connected")
	// ErrScope reports a grant that lacks a scope Wattson needs.
	ErrScope = errors.New("strava: required scope not granted")
)

// ServiceDeps are the collaborators of Service.
type ServiceDeps struct {
	Store    *store.Store
	Box      *secretbox.Box
	Settings *settings.Service
	Client   *Client
	Logger   *slog.Logger
	// PollEvery and PollMax pace upload status polling (2 s and 2 min when zero).
	PollEvery, PollMax time.Duration
	Now                func() time.Time
}

// Service manages riders' Strava links: app credentials, the OAuth connect
// flow and sealed tokens.
type Service struct {
	d        ServiceDeps
	stateKey []byte
	mu       sync.Mutex // serialises read-modify-write of strava_links rows
}

// NewService builds a Service.
func NewService(d ServiceDeps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.PollEvery == 0 {
		d.PollEvery = 2 * time.Second
	}
	if d.PollMax == 0 {
		d.PollMax = 2 * time.Minute
	}
	return &Service{d: d, stateKey: d.Box.Derive(stateLabel)}
}

// Seal AADs bind each sealed value to its rider and field.
func aad(riderID int64, field string) string {
	return "strava.rider." + strconv.FormatInt(riderID, 10) + "." + field
}

// AppFor returns the app credentials for riderID: the rider's own when the
// link has a client ID (own is true; an own app without a secret is
// ErrNotConfigured, it never falls back silently), else the shared app from
// the settings.
func (s *Service) AppFor(ctx context.Context, riderID int64) (App, bool, error) {
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return App{}, false, err
	}
	if l.ClientID != "" {
		secret, err := s.d.Box.OpenFor(l.ClientSecretEnc, aad(riderID, "client_secret"))
		if err != nil {
			return App{}, true, fmt.Errorf("strava: open client secret: %w", err)
		}
		if secret == "" {
			return App{}, true, ErrNotConfigured
		}
		return App{ClientID: l.ClientID, ClientSecret: secret}, true, nil
	}
	cfg, err := s.d.Settings.Get(ctx)
	if err != nil {
		return App{}, false, err
	}
	if cfg.Strava.ClientID == "" || cfg.Strava.ClientSecret == "" {
		return App{}, false, ErrNotConfigured
	}
	return App{ClientID: cfg.Strava.ClientID, ClientSecret: cfg.Strava.ClientSecret}, false, nil
}

// ConnectURL is the Strava authorize URL for riderID, carrying a signed
// state bound to userID and riderID. returnTo must already be a same-origin
// path.
func (s *Service) ConnectURL(ctx context.Context, riderID, userID int64, redirectURI, returnTo string) (string, error) {
	app, _, err := s.AppFor(ctx, riderID)
	if err != nil {
		return "", err
	}
	state := EncodeState(s.stateKey, ConnectState{RiderID: riderID, UserID: userID, ReturnTo: returnTo}, s.d.Now())
	return s.d.Client.AuthCodeURL(app, redirectURI, state), nil
}

// ReadState verifies state for the signed-in userID without side effects.
func (s *Service) ReadState(state string, userID int64) (ConnectState, error) {
	cs, err := DecodeState(s.stateKey, state, s.d.Now())
	if err != nil {
		return ConnectState{}, err
	}
	if cs.UserID != userID || userID == 0 {
		return ConnectState{}, ErrState
	}
	return cs, nil
}

// Complete finishes a connect: it verifies state against the signed-in
// userID, checks the granted scope, exchanges code and stores the sealed
// tokens. The returned ConnectState is valid whenever err is nil, ErrScope
// included.
func (s *Service) Complete(ctx context.Context, state, code, grantedScope, redirectURI string, userID int64) (ConnectState, error) {
	cs, err := s.ReadState(state, userID)
	if err != nil {
		return ConnectState{}, err
	}
	if _, err := s.d.Store.Rider(ctx, userID, cs.RiderID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ConnectState{}, ErrState
		}
		return ConnectState{}, err
	}
	if !HasScope(grantedScope, "read") || !HasScope(grantedScope, "activity:write") {
		return cs, ErrScope
	}
	app, _, err := s.AppFor(ctx, cs.RiderID)
	if err != nil {
		return cs, err
	}
	ctx, cancel := context.WithTimeout(ctx, exchangeBudget)
	defer cancel()
	g, err := s.d.Client.Exchange(ctx, app, redirectURI, code)
	if err != nil {
		return cs, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, cs.RiderID)
	if err != nil {
		return cs, err
	}
	now := s.d.Now().UTC()
	l.AthleteID, l.AthleteName, l.Scope = g.Athlete.ID, g.Athlete.Name, grantedScope
	l.AccessEnc = s.d.Box.SealFor(g.Token.AccessToken, aad(cs.RiderID, "access_token"))
	l.RefreshEnc = s.d.Box.SealFor(g.Token.RefreshToken, aad(cs.RiderID, "refresh_token"))
	l.ExpiresAt, l.ConnectedAt = g.Token.Expiry.UTC(), &now
	if l.RefreshEnc == "" {
		return cs, errors.New("strava: token response has no refresh token")
	}
	return cs, s.d.Store.PutStravaLink(ctx, l)
}

// Disconnect revokes the rider's authorisation at Strava (best effort) and
// clears the athlete, tokens and connection time. The rider's own app
// credentials and auto_upload are kept.
func (s *Service) Disconnect(ctx context.Context, riderID int64) error {
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return err
	}
	if !l.Connected() {
		return nil
	}
	if ts, err := s.Source(ctx, riderID); err == nil {
		if tok, err := ts.Token(); err == nil {
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deauthTimeout)
			if err := s.d.Client.Deauthorize(dctx, tok.AccessToken); err != nil {
				s.d.Logger.Warn("strava deauthorize failed", "rider", riderID, "err", err)
			}
			cancel()
		}
	}
	return s.Forget(ctx, riderID)
}

// Forget clears the rider's connection locally, without telling Strava (it
// has already revoked the grant, or Disconnect has just asked it to).
func (s *Service) Forget(ctx context.Context, riderID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return err
	}
	l.AthleteID, l.AthleteName, l.Scope = 0, "", ""
	l.AccessEnc, l.RefreshEnc, l.ExpiresAt, l.ConnectedAt = "", "", time.Time{}, nil
	return s.d.Store.PutStravaLink(ctx, l)
}

// UpdateLink changes the rider's own app credentials and auto-upload flag;
// nil fields stay as they are. A changed client ID or secret disconnects the
// rider first (tokens belong to the old app). Emptying the client ID returns
// the rider to the shared app and drops the own secret.
func (s *Service) UpdateLink(ctx context.Context, riderID int64, clientID, clientSecret *string, autoUpload *bool) (store.StravaLink, error) {
	cur, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return store.StravaLink{}, err
	}
	curSecret, err := s.d.Box.OpenFor(cur.ClientSecretEnc, aad(riderID, "client_secret"))
	if err != nil {
		return store.StravaLink{}, fmt.Errorf("strava: open client secret: %w", err)
	}
	newID, newSecret := cur.ClientID, curSecret
	if clientID != nil {
		newID = *clientID
	}
	if clientSecret != nil {
		newSecret = *clientSecret
	}
	if newID == "" {
		newSecret = ""
	}
	if newID != cur.ClientID || newSecret != curSecret {
		if err := s.Disconnect(ctx, riderID); err != nil {
			return store.StravaLink{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return store.StravaLink{}, err
	}
	l.ClientID = newID
	if newSecret != curSecret || l.ClientSecretEnc == "" {
		l.ClientSecretEnc = s.d.Box.SealFor(newSecret, aad(riderID, "client_secret"))
	}
	if autoUpload != nil {
		l.AutoUpload = *autoUpload
	}
	if err := s.d.Store.PutStravaLink(ctx, l); err != nil {
		return store.StravaLink{}, err
	}
	return l, nil
}

// Source returns a token source for the connected rider. A refreshed token
// pair is stored (sealed) before it is served; a refresh Strava refuses
// clears the link and returns ErrUnauthorized.
func (s *Service) Source(ctx context.Context, riderID int64) (oauth2.TokenSource, error) {
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return nil, err
	}
	if !l.Connected() {
		return nil, ErrNotConnected
	}
	app, _, err := s.AppFor(ctx, riderID)
	if err != nil {
		return nil, err
	}
	access, err := s.d.Box.OpenFor(l.AccessEnc, aad(riderID, "access_token"))
	if err != nil {
		return nil, fmt.Errorf("strava: open access token: %w", err)
	}
	refresh, err := s.d.Box.OpenFor(l.RefreshEnc, aad(riderID, "refresh_token"))
	if err != nil {
		return nil, fmt.Errorf("strava: open refresh token: %w", err)
	}
	tok := &oauth2.Token{AccessToken: access, RefreshToken: refresh, Expiry: l.ExpiresAt}
	persist := func(n *oauth2.Token) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur, err := s.d.Store.StravaLink(context.WithoutCancel(ctx), riderID)
		if err != nil {
			return err
		}
		if !cur.Connected() {
			return ErrNotConnected
		}
		cur.AccessEnc = s.d.Box.SealFor(n.AccessToken, aad(riderID, "access_token"))
		cur.RefreshEnc = s.d.Box.SealFor(n.RefreshToken, aad(riderID, "refresh_token"))
		cur.ExpiresAt = n.Expiry.UTC()
		return s.d.Store.PutStravaLink(context.WithoutCancel(ctx), cur)
	}
	return &forgetOnUnauthorized{s: s, riderID: riderID, ts: s.d.Client.TokenSource(ctx, app, tok, persist)}, nil
}

type forgetOnUnauthorized struct {
	s       *Service
	riderID int64
	ts      oauth2.TokenSource
}

func (f *forgetOnUnauthorized) Token() (*oauth2.Token, error) {
	t, err := f.ts.Token()
	if errors.Is(err, ErrUnauthorized) {
		if ferr := f.s.Forget(context.Background(), f.riderID); ferr != nil {
			f.s.d.Logger.Warn("strava: clearing revoked link failed", "rider", f.riderID, "err", ferr)
		}
	}
	return t, err
}
