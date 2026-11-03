package mapillary

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/store"
)

const (
	photosMaxAge  = 30 * 24 * time.Hour // a ready build older than this is rebuilt
	errorRetry    = 10 * time.Minute    // a failed build is retried after this
	buildTimeout  = 10 * time.Minute
	coverageZoom  = 14
	coverageBufM  = 25
	coverageFetch = 4    // concurrent tile requests within one build
	maxTiles      = 1500 // z14 tiles per route (about 2000 km); more is refused
)

// Status is what the photo endpoint reports for a route.
type Status struct {
	State   string // disabled, unavailable, building, ready or error
	Message string // plain-language reason for unavailable and error
	BuiltAt time.Time
	Photos  []Photo // set when ready; never nil then
}

// Messages shown to the user. None carries upstream text.
const (
	msgSynthetic   = "Synthetic routes have no street photos"
	msgRejected    = "Mapillary rejected the token. Check it in Settings → Integrations."
	msgRateLimited = "Mapillary is rate limiting requests. Try again in a few minutes."
	msgUnreachable = "Mapillary could not be reached. Try again in a few minutes."
	msgTileLarge   = "Mapillary coverage along this route is too dense to load. Try a shorter route."
	msgRouteBroken = "The route could not be read."
	msgTooLong     = "This route is too long for street photos"
)

// ErrBuilderClosed is returned by Get after Close.
var ErrBuilderClosed = errors.New("mapillary: builder closed")

// Builder keeps one cached photo list per route, built in the background by
// a single worker goroutine from a deduplicated queue.
type Builder struct {
	logger *slog.Logger

	c    *Client
	st   *store.Store
	load func(ctx context.Context, routeID int64) (*route.Profile, error)
	fp   func(token string) string
	now  func() time.Time

	mu      sync.Mutex
	cur     *job
	queue   []int64
	pending map[int64]struct{} // queued or being built
	// finished counts completed builds. Get compares it before and after
	// reading a route's cache row, so a build that completes in between (its
	// row already stored, its pending mark already cleared) is not queued again
	// from the stale read.
	finished uint64
	closed   bool

	afterRead func() // tests only: runs in Get between reading the cache row and queuing

	wake   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// job is the build in progress.
type job struct {
	id     int64
	cancel context.CancelFunc
	drop   bool // route deleted: discard the result
	redo   bool // credentials changed: discard the result and build again
}

// NewBuilder starts the worker. fp turns a token into the fingerprint kept
// with the cached rows (see NewFingerprint); a changed token rebuilds.
func NewBuilder(c *Client, st *store.Store, load func(ctx context.Context, routeID int64) (*route.Profile, error),
	fp func(token string) string, now func() time.Time, logger *slog.Logger) *Builder {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Builder{logger: logger, c: c, st: st, load: load, fp: fp, now: now, pending: map[int64]struct{}{},
		wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go b.run()
	return b
}

// NewFingerprint returns a token fingerprint function: the first 16 hex
// characters of HMAC-SHA256 under key.
func NewFingerprint(key []byte) func(token string) string {
	return func(token string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(token))
		return hex.EncodeToString(m.Sum(nil))[:16]
	}
}

// Get reports the photo state of a route. A missing, stale (over 30 days) or
// token-mismatched cache, and an error row older than 10 minutes, queue a
// build and answer building. store.ErrNotFound for an unknown route.
func (b *Builder) Get(ctx context.Context, routeID int64) (Status, error) {
	b.mu.Lock()
	closed, seen := b.closed, b.finished
	b.mu.Unlock()
	if closed {
		return Status{}, ErrBuilderClosed
	}
	src, err := b.st.RouteSource(ctx, routeID)
	if err != nil {
		return Status{}, err
	}
	tok, err := b.c.token(ctx)
	if errors.Is(err, ErrNoToken) {
		return Status{State: "disabled"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	if src == "synthetic" {
		return Status{State: "unavailable", Message: msgSynthetic}, nil
	}
	building := Status{State: "building"}
	row, err := b.st.GetRoutePhotos(ctx, routeID)
	if b.afterRead != nil {
		b.afterRead()
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return b.enqueue(routeID, building, seen)
	case err != nil:
		return Status{}, err
	case row.TokenFP != b.fp(tok):
		return b.enqueue(routeID, building, seen)
	}
	age := b.now().Sub(row.BuiltAt)
	switch row.Status {
	case "ready":
		if age > photosMaxAge {
			return b.enqueue(routeID, building, seen)
		}
		ps, err := DecodePhotos(row.Photos)
		if err != nil {
			b.logger.Warn("discarding unreadable cached photos", "route", routeID, "err", err)
			return b.enqueue(routeID, building, seen)
		}
		return Status{State: "ready", BuiltAt: row.BuiltAt, Photos: ps}, nil
	case "error":
		if age >= errorRetry {
			return b.enqueue(routeID, building, seen)
		}
		return Status{State: "error", Message: row.Error, BuiltAt: row.BuiltAt}, nil
	}
	return b.enqueue(routeID, building, seen)
}

// enqueue queues routeID once and returns st. seen is Builder.finished as
// Get started: if a build has completed since, the row Get read may predate
// it, so nothing is queued and the next Get (the page polls while "building")
// reads the fresh row.
func (b *Builder) enqueue(routeID int64, st Status, seen uint64) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Status{}, ErrBuilderClosed
	}
	if _, ok := b.pending[routeID]; !ok && b.finished == seen {
		b.pending[routeID] = struct{}{}
		b.queue = append(b.queue, routeID)
		select {
		case b.wake <- struct{}{}:
		default:
		}
	}
	return st, nil
}

// HasImage reports whether image id is in some route's cached photo list.
// The image proxy serves only those, so it cannot be used to spend the
// token's quota on arbitrary ids.
func (b *Builder) HasImage(ctx context.Context, id string) (bool, error) {
	return b.st.HasPhotoImage(ctx, id)
}

// Cancel forgets routeID: it leaves the queue and a build in progress for it
// is stopped and discarded (call it when the route is deleted).
func (b *Builder) Cancel(routeID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if j := b.cur; j != nil && j.id == routeID {
		j.drop = true
		j.cancel()
	}
	if _, ok := b.pending[routeID]; ok && (b.cur == nil || b.cur.id != routeID) {
		delete(b.pending, routeID)
		b.queue = slices.DeleteFunc(b.queue, func(id int64) bool { return id == routeID })
	}
}

// Reconfigure stops a build in progress and runs it again from the start
// (call it when the Mapillary token or URLs change). Queued builds read the
// settings when they start, so they need nothing.
func (b *Builder) Reconfigure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if j := b.cur; j != nil && !j.drop {
		j.redo = true
		j.cancel()
	}
}

// Close stops the worker, cancelling a build in progress (its result is
// discarded), and waits for it or for ctx.
func (b *Builder) Close(ctx context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.cancel()
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *Builder) run() {
	defer close(b.done)
	for {
		id, ok := b.next()
		if !ok {
			return
		}
		redo := b.build(id)
		b.mu.Lock()
		if redo && !b.closed {
			b.queue = append(b.queue, id) // stays pending
		} else {
			delete(b.pending, id)
		}
		b.finished++
		b.mu.Unlock()
		if redo {
			select {
			case b.wake <- struct{}{}:
			default:
			}
		}
	}
}

func (b *Builder) next() (int64, bool) {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return 0, false
		}
		if len(b.queue) > 0 {
			id := b.queue[0]
			b.queue = b.queue[1:]
			b.mu.Unlock()
			return id, true
		}
		b.mu.Unlock()
		select {
		case <-b.wake:
		case <-b.ctx.Done():
			return 0, false
		}
	}
}

// build runs one job; redo reports that the credentials changed meanwhile
// and the route must be built again.
func (b *Builder) build(id int64) (redo bool) {
	ctx, cancel := context.WithTimeout(b.ctx, buildTimeout)
	defer cancel()
	j := &job{id: id, cancel: cancel}
	b.mu.Lock()
	b.cur = j
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.cur = nil
		redo = j.redo && !j.drop && !b.closed
		b.mu.Unlock()
	}()
	tok, err := b.c.token(ctx)
	if err != nil {
		return false // token removed since the request; the next Get reports disabled
	}
	row := store.RoutePhotos{RouteID: id, TokenFP: b.fp(tok)}
	var photos []Photo
	p, err := b.load(ctx, id)
	if err == nil {
		photos, err = b.fetch(ctx, p)
	} else {
		err = errors.Join(errRouteUnreadable, err)
	}
	b.mu.Lock()
	superseded := j.drop || j.redo
	b.mu.Unlock()
	if b.ctx.Err() != nil || superseded {
		return false // shutting down, route deleted or credentials changed: store nothing
	}
	if err == nil {
		row.Status = "ready"
		if row.Photos, err = EncodePhotos(photos); err != nil {
			row.Photos = nil
		}
		row.ImageIDs = make([]string, len(photos))
		for i, ph := range photos {
			row.ImageIDs[i] = ph.ID
		}
	}
	if err != nil {
		b.logger.Warn("street photo build failed", "route", id, "err", err)
		row.Status = "error"
		switch {
		case errors.Is(err, ErrTokenRejected):
			row.Error = msgRejected
		case errors.Is(err, ErrRateLimited):
			row.Error = msgRateLimited
		case errors.Is(err, ErrTileTooLarge):
			row.Error = msgTileLarge
		case errors.Is(err, errRouteUnreadable):
			row.Error = msgRouteBroken
		case errors.Is(err, errTooLong):
			row.Error = msgTooLong
		default:
			row.Error = msgUnreachable
		}
	}
	row.BuiltAt = b.now()
	sctx, scancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer scancel()
	if err := b.st.PutRoutePhotos(sctx, row); err != nil && !errors.Is(err, store.ErrNotFound) && b.ctx.Err() == nil {
		b.logger.Warn("storing street photos failed", "route", id, "err", err)
	}
	return false
}

var (
	errRouteUnreadable = errors.New("route unreadable")
	errTooLong         = errors.New("route too long")
)

// fetch reads the coverage tiles along p (coverageFetch at a time) and
// selects the photos as the tiles arrive, so only the best photo per bucket
// is kept. Any tile failure fails the whole build.
func (b *Builder) fetch(ctx context.Context, p *route.Profile) ([]Photo, error) {
	tiles := Tiles(p, coverageZoom, coverageBufM)
	if len(tiles) > maxTiles {
		return nil, errTooLong
	}
	sel := NewSelector(p, SelectOpts{})
	if sel == nil {
		return []Photo{}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan [3]int, len(tiles))
	for _, t := range tiles {
		jobs <- t
	}
	close(jobs)

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	for range min(coverageFetch, len(tiles)) {
		wg.Go(func() {
			for t := range jobs {
				if ctx.Err() != nil {
					return
				}
				cs, err := b.c.Coverage(ctx, t[0], t[1], t[2])
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					mu.Unlock()
					return
				}
				sel.Add(cs) // an image near a tile edge may come twice; it lands in the same bucket
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := sel.Photos()
	if out == nil {
		out = []Photo{}
	}
	return out, nil
}
