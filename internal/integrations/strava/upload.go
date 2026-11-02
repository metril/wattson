package strava

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/metril/wattson/internal/integrations/fitexport"
	"github.com/metril/wattson/internal/store"
)

const (
	// maxUploads bounds the uploads in flight at once.
	maxUploads = 4
	// maxErrLen bounds the error text stored on a ride.
	maxErrLen = 300
	// stateWrite bounds one write of a ride's final state.
	stateWrite = 10 * time.Second

	msgRateLimited = "Strava rate limit reached; try again in 15 minutes"
	msgUnauth      = "Strava authorization expired; reconnect Strava for this rider"
	msgStillBusy   = "Strava is still processing; check again later"
	msgInterrupted = "interrupted; retry"
	msgNotConfig   = "Strava isn't configured on this server — ask an admin"
	msgNotSaved    = "could not record the Strava upload; retry"
)

var (
	// ErrRideInProgress reports a ride that is still recording.
	ErrRideInProgress = errors.New("strava: ride is still recording")
	// ErrClosed reports an upload requested while the service shuts down.
	ErrClosed = errors.New("strava: shutting down")
)

// uploader is the worker pool state of a Service.
type uploader struct {
	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	running map[int64]bool // ride IDs with a worker (or a pending claim)
}

func (u *uploader) init() {
	u.ctx, u.cancel = context.WithCancel(context.Background())
	u.sem = make(chan struct{}, maxUploads)
	u.running = map[int64]bool{}
}

func (u *uploader) isClosed() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.closed
}

// reserve marks rideID as having a worker; false when it already has one or
// the service is closed.
func (u *uploader) reserve(rideID int64) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed || u.running[rideID] {
		return false
	}
	u.running[rideID] = true
	return true
}

func (u *uploader) release(rideID int64) {
	u.mu.Lock()
	delete(u.running, rideID)
	u.mu.Unlock()
}

// spawn runs the worker for a reserved rideID; false when closed meanwhile.
func (u *uploader) spawn(rideID int64, fn func()) bool {
	u.mu.Lock()
	if u.closed {
		delete(u.running, rideID)
		u.mu.Unlock()
		return false
	}
	u.wg.Add(1)
	u.mu.Unlock()
	go func() {
		defer u.wg.Done()
		defer u.release(rideID)
		fn()
	}()
	return true
}

// Close stops accepting uploads, cancels the running workers and waits for
// them. A ride whose upload was cut short stays "uploading" and resumes at
// the next start.
func (s *Service) Close() {
	s.up.mu.Lock()
	s.up.closed = true
	s.up.mu.Unlock()
	s.up.cancel()
	s.up.wg.Wait()
}

// Upload uploads the finished ride to the rider's Strava. Only the call that
// atomically moves the ride from none or error to uploading starts a worker
// (started is true); any other call returns the ride's current state. A rider
// who is not connected is ErrNotConnected and the ride is left untouched; a
// missing ride is store.ErrNotFound.
func (s *Service) Upload(ctx context.Context, rideID int64) (st store.StravaState, started bool, err error) {
	if s.up.isClosed() {
		return store.StravaState{}, false, ErrClosed
	}
	r, err := s.d.Store.Ride(ctx, rideID)
	if err != nil {
		return store.StravaState{}, false, err
	}
	if r.EndedAt == nil {
		return r.Strava, false, ErrRideInProgress
	}
	l, err := s.d.Store.StravaLink(ctx, r.RiderID)
	if err != nil {
		return store.StravaState{}, false, err
	}
	if !l.Connected() {
		return r.Strava, false, ErrNotConnected
	}
	if !s.up.reserve(rideID) {
		return r.Strava, false, nil
	}
	won, err := s.d.Store.ClaimStravaUpload(ctx, rideID, s.d.Now().UTC())
	if err != nil || !won {
		s.up.release(rideID)
		if err != nil {
			return store.StravaState{}, false, err
		}
		r, err = s.d.Store.Ride(ctx, rideID)
		return r.Strava, false, err
	}
	if !s.up.spawn(rideID, func() { s.work(rideID, false) }) {
		// Closed since the check: the claim stays and Resume picks it up at
		// the next start.
		return store.StravaState{}, false, ErrClosed
	}
	r, err = s.d.Store.Ride(ctx, rideID)
	return r.Strava, true, err
}

// AutoUpload is the recorder's OnFinished hook: it uploads r when its rider
// is connected and has auto_upload on. Failures are logged.
func (s *Service) AutoUpload(ctx context.Context, r store.Ride) {
	l, err := s.d.Store.StravaLink(ctx, r.RiderID)
	if err != nil {
		s.d.Logger.Warn("strava auto-upload", "ride", r.ID, "err", err)
		return
	}
	if !l.AutoUpload || !l.Connected() {
		return
	}
	if _, _, err := s.Upload(ctx, r.ID); err != nil && !errors.Is(err, ErrClosed) {
		s.d.Logger.Warn("strava auto-upload", "ride", r.ID, "err", err)
	}
}

// Resume restarts the workers of rides left "uploading" by a previous run:
// those with an upload ID poll it. Those without one may or may not have
// reached Strava: when the rider is connected with auto_upload on they are
// sent again (Strava answers a repeat with the existing activity), otherwise
// they are marked failed ("interrupted; retry").
func (s *Service) Resume(ctx context.Context) {
	rs, err := s.d.Store.RidesUploading(ctx)
	if err != nil {
		s.d.Logger.Warn("strava resume", "err", err)
		return
	}
	for _, r := range rs {
		id := r.ID
		resend := false
		if r.Strava.UploadID == 0 {
			l, err := s.d.Store.StravaLink(ctx, r.RiderID)
			resend = err == nil && l.AutoUpload && l.Connected()
		}
		if s.up.reserve(id) {
			s.up.spawn(id, func() { s.work(id, !resend) })
		}
	}
}

// tokenRec remembers the last access token served, so a 401 can be tied to
// the token that provoked it.
type tokenRec struct {
	src  oauth2.TokenSource
	last string
}

func (t *tokenRec) Token() (*oauth2.Token, error) {
	tok, err := t.src.Token()
	if err == nil {
		t.last = tok.AccessToken
	}
	return tok, err
}

// work runs one ride's upload to a terminal state, or until shutdown.
func (s *Service) work(rideID int64, resume bool) {
	ctx := s.up.ctx
	select {
	case s.up.sem <- struct{}{}:
		defer func() { <-s.up.sem }()
	case <-ctx.Done():
		return
	}
	r, err := s.d.Store.Ride(ctx, rideID)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, store.ErrNotFound) {
			s.d.Logger.Warn("strava upload: load ride", "ride", rideID, "err", err)
		}
		return // deleted: nothing left to report
	}
	if r.Strava.Status != "uploading" {
		return
	}
	uploadID := r.Strava.UploadID
	if resume && uploadID == 0 {
		s.finish(rideID, store.StravaState{Status: "error", Error: msgInterrupted})
		return
	}
	src, err := s.Source(ctx, r.RiderID)
	if err != nil {
		s.fail(ctx, r, nil, err, 0)
		return
	}
	ts := &tokenRec{src: src}

	var up Upload
	delay := s.d.PollEvery
	if uploadID == 0 {
		if up, err = s.send(ctx, ts, r); err != nil {
			s.fail(ctx, r, ts, err, 0)
			return
		}
		uploadID = up.ID
		if err := s.write(rideID, store.StravaState{Status: "uploading", UploadID: uploadID}); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.finish(rideID, store.StravaState{Status: "error", UploadID: uploadID, Error: msgNotSaved})
			}
			return
		}
	} else {
		up, delay = Upload{ID: uploadID}, 0
	}

	pctx, cancel := context.WithTimeout(ctx, s.d.PollMax)
	defer cancel()
	for {
		switch {
		case up.ActivityID != 0:
			s.finish(rideID, store.StravaState{Status: "done", UploadID: uploadID, ActivityID: up.ActivityID})
			return
		case up.Error != "":
			if id, ok := DuplicateActivityID(up.Error); ok {
				s.finish(rideID, store.StravaState{Status: "done", UploadID: uploadID, ActivityID: id})
				return
			}
			s.finish(rideID, store.StravaState{Status: "error", UploadID: uploadID, Error: clip(up.Error)})
			return
		}
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-pctx.Done():
			t.Stop()
		}
		delay = s.d.PollEvery
		if ctx.Err() != nil {
			return // shutdown: stay uploading, resume next start
		}
		if pctx.Err() != nil {
			s.finish(rideID, store.StravaState{Status: "error", UploadID: uploadID, Error: msgStillBusy})
			return
		}
		if _, err := s.d.Store.Ride(ctx, rideID); errors.Is(err, store.ErrNotFound) {
			return // deleted while uploading: terminal, nothing to report
		}
		if up, err = s.d.Client.UploadStatus(pctx, ts, uploadID); err != nil {
			switch {
			case ctx.Err() != nil:
				return
			case pctx.Err() != nil:
				s.finish(rideID, store.StravaState{Status: "error", UploadID: uploadID, Error: msgStillBusy})
				return
			case transient(err):
				s.d.Logger.Debug("strava upload poll failed; retrying", "ride", rideID, "err", err)
				up = Upload{ID: uploadID}
			default:
				s.fail(ctx, r, ts, err, uploadID)
				return
			}
		}
	}
}

// transient reports whether a status-poll error is worth retrying until the
// deadline: network failures and 5xx answers. Authorisation, rate limits and
// other 4xx answers are final.
func transient(err error) bool {
	var se *StatusError
	switch {
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrRateLimited), errors.Is(err, ErrNotConnected), errors.Is(err, ErrNotConfigured):
		return false
	case errors.As(err, &se):
		return se.Code >= 500
	}
	return true
}

// send encodes the ride and uploads it.
func (s *Service) send(ctx context.Context, ts oauth2.TokenSource, r store.Ride) (Upload, error) {
	ss, err := s.d.Store.Samples(ctx, r.ID)
	if err != nil {
		return Upload{}, err
	}
	var buf bytes.Buffer
	if err := fitexport.Encode(&buf, r, ss); err != nil {
		return Upload{}, err
	}
	name := r.RouteName
	if name == "" {
		name = "Wattson ride"
	}
	return s.d.Client.Upload(ctx, ts, UploadReq{FIT: buf.Bytes(), Filename: fitexport.Filename(r), Name: name,
		Description: "Recorded with Wattson", Trainer: true,
		ExternalID: "wattson-" + strconv.FormatInt(r.ID, 10) + "-" + strconv.FormatInt(r.StartedAt.Unix(), 10)})
}

// fail records err on the ride in words fit for the rider (no tokens, no
// internals beyond Strava's own message) and drops a revoked link.
func (s *Service) fail(ctx context.Context, r store.Ride, ts *tokenRec, err error, uploadID int64) {
	if ctx.Err() != nil {
		return // shutdown cut the call short; stay uploading
	}
	st := store.StravaState{Status: "error", UploadID: uploadID}
	switch {
	case errors.Is(err, ErrRateLimited):
		st.Error = msgRateLimited
	case errors.Is(err, ErrNotConfigured):
		st.Error = msgNotConfig
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrNotConnected):
		st.Error = msgUnauth
		if ts != nil && ts.last != "" && errors.Is(err, ErrUnauthorized) {
			rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWrite)
			if rerr := s.Revoked(rctx, r.RiderID, ts.last); rerr != nil {
				s.d.Logger.Warn("strava: clearing revoked link failed", "rider", r.RiderID, "err", rerr)
			}
			cancel()
		}
	default:
		st.Error = clip(err.Error())
		s.d.Logger.Warn("strava upload failed", "ride", r.ID, "err", err)
	}
	s.finish(r.ID, st)
}

// finish writes a terminal state.
func (s *Service) finish(rideID int64, st store.StravaState) { _ = s.write(rideID, st) }

// write stores st even while shutting down. A deleted ride is
// store.ErrNotFound, the end of the story rather than a failure; any other
// error is logged here and returned.
func (s *Service) write(rideID int64, st store.StravaState) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.up.ctx), stateWrite)
	defer cancel()
	st.UpdatedAt = s.d.Now().UTC()
	err := s.d.Store.SetStravaState(ctx, rideID, st)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.d.Logger.Warn("strava upload: save state", "ride", rideID, "err", err)
	}
	return err
}

func clip(s string) string {
	if len(s) > maxErrLen {
		return s[:maxErrLen]
	}
	return s
}
