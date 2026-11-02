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

	smu     sync.Mutex // guards sources
	sources map[int64]*riderSource
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
	return &Service{d: d, stateKey: d.Box.Derive(stateLabel), sources: map[int64]*riderSource{}}
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
	prior, err := s.save(ctx, cs.RiderID, g, grantedScope)
	if err != nil {
		return cs, err
	}
	// A different athlete's grant that this one replaces is orphaned: revoke
	// it. (The same athlete's grant must stay: it is the one just issued.)
	if prior.Connected() && prior.AthleteID != g.Athlete.ID {
		s.deauthorize(ctx, prior)
	}
	return cs, nil
}

// maxScope bounds the stored scope string.
const maxScope = 256

// save stores a fresh grant, returning the link it replaced.
func (s *Service) save(ctx context.Context, riderID int64, g Grant, browserScope string) (store.StravaLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return store.StravaLink{}, err
	}
	prior := l
	scope := g.Scope
	if scope == "" {
		scope = browserScope
	}
	if len(scope) > maxScope {
		scope = scope[:maxScope]
	}
	now := s.d.Now().UTC()
	l.AthleteID, l.AthleteName, l.Scope = g.Athlete.ID, g.Athlete.Name, scope
	l.AccessEnc = s.d.Box.SealFor(g.Token.AccessToken, aad(riderID, "access_token"))
	l.RefreshEnc = s.d.Box.SealFor(g.Token.RefreshToken, aad(riderID, "refresh_token"))
	l.ExpiresAt, l.ConnectedAt = g.Token.Expiry.UTC(), &now
	if l.RefreshEnc == "" {
		return store.StravaLink{}, errors.New("strava: token response has no refresh token")
	}
	if err := s.d.Store.PutStravaLink(ctx, l); err != nil {
		return store.StravaLink{}, err
	}
	s.dropSource(riderID)
	return prior, nil
}

// deauthorize revokes l's access token at Strava, best effort within a
// bounded budget. An expired token is tried as is: refreshing would spend
// the budget on a call that only helps a grant about to be dropped.
func (s *Service) deauthorize(ctx context.Context, l store.StravaLink) {
	access, err := s.d.Box.OpenFor(l.AccessEnc, aad(l.RiderID, "access_token"))
	if err != nil || access == "" {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deauthTimeout)
	defer cancel()
	if err := s.d.Client.Deauthorize(dctx, access); err != nil {
		s.d.Logger.Warn("strava deauthorize failed", "rider", l.RiderID, "err", err)
	}
}

// Disconnect revokes the rider's authorisation at Strava (best effort, at
// most 10 s) and clears the athlete, tokens and connection time. The rider's
// own app credentials and auto_upload are kept.
func (s *Service) Disconnect(ctx context.Context, riderID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disconnectLocked(ctx, riderID)
}

func (s *Service) disconnectLocked(ctx context.Context, riderID int64) error {
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return err
	}
	if !l.Connected() {
		return nil
	}
	s.deauthorize(ctx, l)
	return s.clearLocked(ctx, l)
}

// Forget clears the rider's connection locally, without telling Strava (it
// has already revoked the grant). Prefer Revoked after a 401, which refuses
// to clear a connection newer than the token that failed.
func (s *Service) Forget(ctx context.Context, riderID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return err
	}
	return s.clearLocked(ctx, l)
}

// Revoked clears the rider's connection after Strava answered 401 to
// accessToken, but only if that token is still the stored one: a newer
// connection or rotation is left alone.
func (s *Service) Revoked(ctx context.Context, riderID int64, accessToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return err
	}
	cur, err := s.d.Box.OpenFor(l.AccessEnc, aad(riderID, "access_token"))
	if err != nil || cur != accessToken {
		return nil
	}
	return s.clearLocked(ctx, l)
}

// clearLocked wipes l's connection fields (s.mu held).
func (s *Service) clearLocked(ctx context.Context, l store.StravaLink) error {
	l.AthleteID, l.AthleteName, l.Scope = 0, "", ""
	l.AccessEnc, l.RefreshEnc, l.ExpiresAt, l.ConnectedAt = "", "", time.Time{}, nil
	s.dropSource(l.RiderID)
	return s.d.Store.PutStravaLink(ctx, l)
}

// UpdateLink changes the rider's own app credentials and auto-upload flag;
// nil fields stay as they are. A changed client ID or secret disconnects the
// rider first (tokens belong to the old app). Emptying the client ID returns
// the rider to the shared app and drops the own secret.
func (s *Service) UpdateLink(ctx context.Context, riderID int64, clientID, clientSecret *string, autoUpload *bool) (store.StravaLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return store.StravaLink{}, err
	}
	curSecret, err := s.d.Box.OpenFor(l.ClientSecretEnc, aad(riderID, "client_secret"))
	if err != nil {
		return store.StravaLink{}, fmt.Errorf("strava: open client secret: %w", err)
	}
	newID, newSecret := l.ClientID, curSecret
	if clientID != nil {
		newID = *clientID
	}
	if clientSecret != nil {
		newSecret = *clientSecret
	}
	if newID == "" {
		newSecret = ""
	}
	if newID != l.ClientID || newSecret != curSecret {
		if l.Connected() {
			s.deauthorize(ctx, l)
			if err := s.clearLocked(ctx, l); err != nil {
				return store.StravaLink{}, err
			}
			if l, err = s.d.Store.StravaLink(ctx, riderID); err != nil {
				return store.StravaLink{}, err
			}
		}
		l.ClientID = newID
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

// Source returns the rider's cached token source. Every Token call starts
// from the stored (sealed) link, so a source that outlives a reconnect, a
// rotation by another source or a disconnect never serves or overwrites
// stale tokens. A rotated token pair is stored before it is returned. A
// refresh Strava refuses clears the link, but only if the refresh token that
// was refused is still the stored one; the error is then ErrUnauthorized.
func (s *Service) Source(ctx context.Context, riderID int64) (oauth2.TokenSource, error) {
	l, err := s.d.Store.StravaLink(ctx, riderID)
	if err != nil {
		return nil, err
	}
	if !l.Connected() {
		return nil, ErrNotConnected
	}
	s.smu.Lock()
	defer s.smu.Unlock()
	src := s.sources[riderID]
	if src == nil {
		src = &riderSource{s: s, riderID: riderID}
		s.sources[riderID] = src
	}
	return src, nil
}

// dropSource forgets the cached source after the link changed hands.
func (s *Service) dropSource(riderID int64) {
	s.smu.Lock()
	delete(s.sources, riderID)
	s.smu.Unlock()
}

type riderSource struct {
	s       *Service
	riderID int64
	mu      sync.Mutex // one refresh at a time per source
}

// expiryDelta matches oauth2's refresh-early window.
const expiryDelta = 10 * time.Second

func (r *riderSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.s
	ctx, cancel := context.WithTimeout(context.Background(), exchangeBudget)
	defer cancel()
	// Two passes: the second runs when another writer changed the link while
	// this one was refreshing, and then trusts what the store holds.
	for pass := 0; ; pass++ {
		l, err := s.d.Store.StravaLink(ctx, r.riderID)
		if err != nil {
			return nil, err
		}
		if !l.Connected() {
			return nil, ErrNotConnected
		}
		access, err := s.d.Box.OpenFor(l.AccessEnc, aad(r.riderID, "access_token"))
		if err != nil {
			return nil, fmt.Errorf("strava: open access token: %w", err)
		}
		refresh, err := s.d.Box.OpenFor(l.RefreshEnc, aad(r.riderID, "refresh_token"))
		if err != nil {
			return nil, fmt.Errorf("strava: open refresh token: %w", err)
		}
		tok := &oauth2.Token{AccessToken: access, RefreshToken: refresh, Expiry: l.ExpiresAt}
		if access != "" && (l.ExpiresAt.IsZero() || s.d.Now().Add(expiryDelta).Before(l.ExpiresAt)) {
			return tok, nil
		}
		app, _, err := s.AppFor(ctx, r.riderID)
		if err != nil {
			return nil, err
		}
		nt, rerr := s.d.Client.TokenSource(ctx, app, tok, nil).Token()

		s.mu.Lock()
		cur, err := s.d.Store.StravaLink(ctx, r.riderID)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if cur.RefreshEnc != l.RefreshEnc { // someone else rotated, reconnected or disconnected
			s.mu.Unlock()
			if pass < 2 {
				continue
			}
			return nil, errors.New("strava: link changed while refreshing")
		}
		if rerr != nil {
			if errors.Is(rerr, ErrUnauthorized) {
				if err := s.clearLocked(ctx, cur); err != nil {
					s.d.Logger.Warn("strava: clearing revoked link failed", "rider", r.riderID, "err", err)
				}
			}
			s.mu.Unlock()
			return nil, rerr
		}
		cur.AccessEnc = s.d.Box.SealFor(nt.AccessToken, aad(r.riderID, "access_token"))
		cur.RefreshEnc = s.d.Box.SealFor(nt.RefreshToken, aad(r.riderID, "refresh_token"))
		cur.ExpiresAt = nt.Expiry.UTC()
		err = s.d.Store.PutStravaLink(ctx, cur)
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nt, nil
	}
}
