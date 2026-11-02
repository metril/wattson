package history

import (
	"context"
	"io"
	"log/slog"
	"math"
	"time"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/store"
)

const (
	defaultFlushEvery = 10 * time.Second
	defaultMinSamples = 10
	maxBuffered       = 600 // samples kept across failed flushes
	finalizeTimeout   = 5 * time.Second
)

// RideStore is the part of *store.Store the recorder writes to.
type RideStore interface {
	CreateRide(ctx context.Context, r store.Ride) (store.Ride, error)
	AppendSamples(ctx context.Context, rideID int64, ss []store.Sample) error
	Samples(ctx context.Context, rideID int64) ([]store.Sample, error)
	FinishRide(ctx context.Context, id int64, endedAt time.Time, sum store.Summary, n int) (store.Ride, error)
	DeleteRide(ctx context.Context, id int64) error
}

// RecorderDeps are the recorder's collaborators.
type RecorderDeps struct {
	Store RideStore
	// RouteInfo names a route and returns its profile; nil or an error means
	// no position and an empty name.
	RouteInfo func(ctx context.Context, id int64) (name string, p *route.Profile, err error)
	// OnFinished runs after a ride is finished; it must not block.
	OnFinished func(ctx context.Context, r store.Ride)
	FlushEvery time.Duration // default 10s
	MinSamples int           // default 10; shorter rides are deleted
	Logger     *slog.Logger
}

// Recorder turns the ride event stream into stored rides with 1 Hz samples.
type Recorder struct {
	d     RecorderDeps
	rider int64

	cur     *store.Ride
	prof    *route.Profile
	buf     []store.Sample
	nextSeq int
	lastSec int
	last    store.Sample // newest recorded sample
	warned  bool         // begin already warned for this attempt
}

// NewRecorder returns a recorder with defaults applied.
func NewRecorder(d RecorderDeps) *Recorder {
	if d.FlushEvery <= 0 {
		d.FlushEvery = defaultFlushEvery
	}
	if d.MinSamples <= 0 {
		d.MinSamples = defaultMinSamples
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Recorder{d: d}
}

// Run consumes events until ctx ends or events is closed. Store calls use a
// context detached from ctx: cancelling it only stops the loop, after which
// the events still buffered are handled and the open ride is finalized (a
// cancelled context would fail every store call and strand the ride). It
// returns ctx.Err() then, or nil when events is closed.
func (r *Recorder) Run(ctx context.Context, events <-chan ridectl.Event) error {
	sctx := context.WithoutCancel(ctx)
	t := time.NewTicker(r.d.FlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.drain(sctx, events)
			if r.cur != nil {
				r.finalize(sctx)
			}
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				if r.cur != nil {
					r.finalize(sctx)
				}
				return nil
			}
			r.handle(sctx, ev)
		case <-t.C:
			r.flush(sctx)
		}
	}
}

// drain handles the events already queued, without waiting for more.
func (r *Recorder) drain(ctx context.Context, events <-chan ridectl.Event) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			r.handle(ctx, ev)
		default:
			return
		}
	}
}

func (r *Recorder) handle(ctx context.Context, ev ridectl.Event) {
	switch {
	case ev.Type == "status" && ev.Status != nil:
		r.rider = ev.Status.RiderID
		if r.cur != nil && ev.Status.State == string(engine.StateIdle) {
			r.finalize(ctx)
		}
	case ev.Type == "tick" && ev.Tick != nil:
		r.tick(ctx, *ev.Tick)
	}
}

func (r *Recorder) tick(ctx context.Context, tk engine.Tick) {
	switch tk.State {
	case engine.StatePaused:
		return
	case engine.StateRiding:
		if r.cur == nil && !r.begin(ctx, tk) {
			return
		}
		r.record(tk)
	default:
		r.warned = false
		if r.cur != nil {
			r.record(tk)
			r.finalize(ctx)
		}
	}
}

// begin opens a ride for the first riding tick; false when it could not.
func (r *Recorder) begin(ctx context.Context, tk engine.Tick) bool {
	if r.rider == 0 {
		r.warn("riding without a rider; ride not recorded")
		return false
	}
	nr := store.Ride{RiderID: r.rider, RouteID: tk.RouteID, Mode: tk.Mode, StartedAt: tk.At}
	var prof *route.Profile
	if tk.RouteID != 0 && r.d.RouteInfo != nil {
		name, p, err := r.d.RouteInfo(ctx, tk.RouteID)
		if err != nil || p == nil {
			r.d.Logger.Warn("route unavailable; recording without position", "route", tk.RouteID, "err", err)
			nr.RouteID = 0
		} else {
			nr.RouteName, prof = name, p
		}
	}
	created, err := r.d.Store.CreateRide(ctx, nr)
	if err != nil {
		r.warn("create ride", "err", err)
		return false
	}
	r.warned = false
	r.cur, r.prof = &created, prof
	r.buf, r.nextSeq, r.lastSec = nil, 0, -1
	return true
}

// warn logs a failed ride start once per attempt, not once per tick.
func (r *Recorder) warn(msg string, args ...any) {
	if !r.warned {
		r.warned = true
		r.d.Logger.Warn(msg, args...)
	}
}

// record appends a sample when tk starts a new second of riding time.
func (r *Recorder) record(tk engine.Tick) {
	sec := int(math.Floor(tk.ElapsedS))
	if sec <= r.lastSec {
		return
	}
	s := store.Sample{
		Seq: r.nextSeq, At: tk.At, ElapsedS: tk.ElapsedS, DistanceM: tk.DistanceM, SpeedKmh: tk.SpeedKmh,
		PowerW: tk.PowerW, CadenceRpm: tk.CadenceRpm, GradePct: tk.GradePct, Level: tk.Level, HRBpm: tk.HeartRateBpm,
		Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN(),
	}
	if r.prof != nil {
		s.Lat, s.Lon = r.prof.PosAt(tk.DistanceM)
		s.EleM = r.prof.EleAt(tk.DistanceM)
	}
	r.nextSeq++
	r.lastSec = sec
	r.last = s
	r.buf = append(r.buf, s)
}

func (r *Recorder) flush(ctx context.Context) bool {
	if r.cur == nil || len(r.buf) == 0 {
		return true
	}
	if err := r.d.Store.AppendSamples(ctx, r.cur.ID, r.buf); err != nil {
		r.d.Logger.Warn("flush samples; will retry", "ride", r.cur.ID, "buffered", len(r.buf), "err", err)
		if over := len(r.buf) - maxBuffered; over > 0 {
			r.d.Logger.Warn("sample buffer full; dropping oldest", "ride", r.cur.ID, "dropped", over)
			r.buf = append(r.buf[:0], r.buf[over:]...)
		}
		return false
	}
	r.buf = r.buf[:0]
	return true
}

// finalize closes the open ride: too short is deleted, otherwise it is
// finished with its summary and handed to OnFinished.
func (r *Recorder) finalize(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
	defer cancel()
	ride := *r.cur
	if !r.flush(ctx) {
		r.d.Logger.Warn("final flush failed; ride left open for crash recovery", "ride", ride.ID)
		r.reset()
		return
	}
	defer r.reset()
	ss, err := r.d.Store.Samples(ctx, ride.ID)
	if err != nil {
		r.d.Logger.Warn("read samples; ride left open for crash recovery", "ride", ride.ID, "err", err)
		return
	}
	if len(ss) < r.d.MinSamples {
		if err := r.d.Store.DeleteRide(ctx, ride.ID); err != nil {
			r.d.Logger.Warn("delete short ride", "ride", ride.ID, "err", err)
		}
		return
	}
	fin, err := r.d.Store.FinishRide(ctx, ride.ID, ss[len(ss)-1].At, Summarize(ss), len(ss))
	if err != nil {
		r.d.Logger.Warn("finish ride", "ride", ride.ID, "err", err)
		return
	}
	if r.d.OnFinished != nil {
		r.d.OnFinished(ctx, fin)
	}
}

func (r *Recorder) reset() {
	r.cur, r.prof, r.buf = nil, nil, nil
}
