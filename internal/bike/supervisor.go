package bike

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/metril/wattson/internal/blue"
)

const (
	supTelemetryBuf = 16
	supEventBuf     = 32
	supObsBuf       = 64 // per-session samples queued for the serve loop
	supCloseTimeout = 5 * time.Second
	supDefaultMax   = 16 // MaxLevel before the first connect when none is configured
)

// SupervisorOptions configures NewSupervisor. Zero values select defaults.
type SupervisorOptions struct {
	MinLevel, MaxLevel int           // floor max(1,MinLevel); MaxLevel 0 = session's (16 before first connect)
	MinWriteInterval   time.Duration // default 1s
	ConfirmWindow      time.Duration // default 2s
	WatchdogTimeout    time.Duration // default 30s
	BackoffMin         time.Duration // default 1s
	BackoffMax         time.Duration // default 30s
}

// Supervisor is a Driver that keeps one Session alive across reconnects and
// guarantees every level reaching it is in [floor, max].
type Supervisor struct {
	dial  Dial
	o     SupervisorOptions
	floor int

	tel chan Telemetry
	ev  chan Event
	// kick wakes run after a target change.
	kick chan struct{}
	// wdArm tells the watchdog a sample with Cadence > 0 arrived.
	wdArm chan struct{}
	bg    sync.WaitGroup // watchdog goroutine

	mu     sync.Mutex
	target int
	// forceLevel (0 = none) is a level the serve loop must write exactly,
	// now, bypassing the write rate limit (set by the watchdog). Later
	// targets go through the gated path.
	forceLevel int
	caps       Caps
	sess       Session // current session; closed by whoever clears it
	started    bool
	closed     bool
	cancel     context.CancelFunc
	exited     chan struct{} // closed when run (and the channels) are finished

	// floorErr is the outcome of the final floor write: ErrNoSession until a
	// live session received it (guarded by mu).
	floorErr error

	closeOnce sync.Once
}

var _ Driver = (*Supervisor)(nil)

// NewSupervisor returns an idle Supervisor; call Connect to start it.
func NewSupervisor(dial Dial, o SupervisorOptions) *Supervisor {
	if o.MinWriteInterval <= 0 {
		o.MinWriteInterval = time.Second
	}
	if o.ConfirmWindow <= 0 {
		o.ConfirmWindow = 2 * time.Second
	}
	if o.WatchdogTimeout <= 0 {
		o.WatchdogTimeout = 30 * time.Second
	}
	if o.BackoffMin <= 0 {
		o.BackoffMin = time.Second
	}
	if o.BackoffMax <= 0 {
		o.BackoffMax = 30 * time.Second
	}
	if o.BackoffMax < o.BackoffMin {
		o.BackoffMax = o.BackoffMin
	}
	floor := o.MinLevel
	if floor < 1 {
		floor = 1
	}
	max := o.MaxLevel
	if max <= 0 {
		max = supDefaultMax
	}
	return &Supervisor{
		dial:   dial,
		o:      o,
		floor:  floor,
		tel:    make(chan Telemetry, supTelemetryBuf),
		ev:     make(chan Event, supEventBuf),
		kick:   make(chan struct{}, 1),
		wdArm:  make(chan struct{}, 1),
		target: ClampLevel(floor, floor, max),
		caps:   Caps{MinLevel: ClampLevel(floor, floor, max), MaxLevel: max},
		exited: make(chan struct{}),

		floorErr: ErrNoSession,
	}
}

// Connect starts supervision and does not block. Cancelling ctx stops it like
// Close. A second call is a no-op.
func (s *Supervisor) Connect(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.started {
		return nil
	}
	s.started = true
	rctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.bg.Add(1)
	go s.watchdog(rctx)
	go s.run(rctx)
	return nil
}

// ArmWatchdog restarts the cadence watchdog clock, so a ride's 30 s grace
// begins when it starts or resumes rather than when the bike connected.
func (s *Supervisor) ArmWatchdog() {
	select {
	case s.wdArm <- struct{}{}:
	default:
	}
}

// SetLevel clamps level into [floor, max], stores it as the target and wakes
// the run loop. It returns the clamped target; the write is asynchronous.
func (s *Supervisor) SetLevel(_ context.Context, level int) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, ErrClosed
	}
	level = ClampLevel(level, s.floor, s.caps.MaxLevel)
	s.target = level
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return level, nil
}

func (s *Supervisor) Telemetry() <-chan Telemetry { return s.tel }
func (s *Supervisor) Events() <-chan Event        { return s.ev }

func (s *Supervisor) Caps() Caps {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.caps
}

// ErrNoSession is returned by Close when supervision had started but no live
// session received the floor level (the link was down or reconnecting), so the
// bike may still be at its last level. Close also returns the floor write's
// own error when a live session rejected it. Close before Connect returns nil.
var ErrNoSession = errors.New("bike: no live session to return to the minimum level")

// Close stops supervision, sends the floor level to a connected session and
// closes Telemetry and Events. It is idempotent and returns the same outcome
// on every call: nil only when a live session accepted the floor write (see
// ErrNoSession).
func (s *Supervisor) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		started := s.started
		s.mu.Unlock()
		if !started {
			return nil
		}
		<-s.exited
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.floorErr
	}
	s.closed = true
	started := s.started
	sess := s.sess
	s.sess = nil
	s.caps.Connected = false
	floor := s.caps.MinLevel
	cancel := s.cancel
	s.mu.Unlock()

	if !started {
		close(s.tel)
		close(s.ev)
		return nil
	}
	cancel()
	if sess != nil {
		// Write the floor explicitly rather than relying on the session's own
		// minimum. A dead link fails here; Close still releases the session.
		_, werr := sess.Resend(ctx, floorFor(sess, floor))
		s.setFloor(floorOutcome(werr, sess.Close(ctx)))
	}
	<-s.exited
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.floorErr
}

// setFloor records the outcome of the final floor write.
func (s *Supervisor) setFloor(err error) {
	s.mu.Lock()
	s.floorErr = err
	s.mu.Unlock()
}

// floorOutcome folds a floor write and session close into one result: a write
// that only failed because the link was gone means the floor was not delivered.
func floorOutcome(werr, cerr error) error {
	if errors.Is(werr, ErrClosed) || errors.Is(werr, blue.ErrDisconnected) {
		werr = fmt.Errorf("%w: %w", ErrNoSession, werr)
	}
	return errors.Join(werr, cerr)
}

// push sends v, dropping the oldest buffered value when ch is full. A channel
// may have more than one producer (s.ev does); the loop retries until v is
// buffered, so a racing producer can at worst cost another oldest value.
func push[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

func (s *Supervisor) emit(e Event) {
	e.At = time.Now()
	push(s.ev, e)
}

func (s *Supervisor) run(ctx context.Context) {
	var fwd sync.WaitGroup
	defer func() {
		// Parent-ctx cancellation: nobody else closed the session.
		s.mu.Lock()
		s.closed = true
		sess := s.sess
		s.sess = nil
		s.caps.Connected = false
		floor := s.caps.MinLevel
		s.mu.Unlock()
		if sess != nil {
			s.closeWithFloor(sess, floor)
		}
		fwd.Wait()
		s.bg.Wait() // the watchdog may still emit
		close(s.tel)
		close(s.ev)
		close(s.exited)
	}()

	backoff := s.o.BackoffMin
	nextWait := func() time.Duration {
		w := backoff
		backoff = min(backoff*2, s.o.BackoffMax)
		return w
	}
	// Backoff resets whenever a session accepts a write, so a link that
	// dies on its first write keeps backing off.
	resetBackoff := func() { backoff = s.o.BackoffMin }
	var wait time.Duration
	for {
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
		sess, err := s.dial(ctx)
		if ctx.Err() != nil {
			if sess != nil {
				s.closeWithFloor(sess, s.floor)
			}
			return
		}
		if err != nil {
			s.emit(Event{Kind: EventDisconnected, Err: err})
			wait = nextWait()
			if errors.Is(err, ErrConfig) { // retrying sooner cannot help
				wait, backoff = s.o.BackoffMax, s.o.BackoffMax
			}
			continue
		}
		if !s.serve(ctx, sess, &fwd, resetBackoff) {
			return
		}
		wait = nextWait()
	}
}

// closeBounded closes a session nobody else is waiting on.
func closeBounded(sess Session) {
	ctx, cancel := context.WithTimeout(context.Background(), supCloseTimeout)
	defer cancel()
	_ = sess.Close(ctx)
}

// floorFor clamps the rider's floor to the session's range.
func floorFor(sess Session, floor int) int {
	return ClampLevel(floor, floor, sess.MaxLevel())
}

// closeWithFloor writes the floor level explicitly (not relying on the
// session's own minimum), then closes the session.
func (s *Supervisor) closeWithFloor(sess Session, floor int) {
	ctx, cancel := context.WithTimeout(context.Background(), supCloseTimeout)
	defer cancel()
	_, werr := sess.Resend(ctx, floorFor(sess, floor))
	s.setFloor(floorOutcome(werr, sess.Close(ctx)))
}

// serve owns one connected session until it ends. It returns false when ctx
// was cancelled (supervision is over). A write error on a session that is
// still alive (e.g. the bike rejecting a level) keeps the session; the write
// is retried once and reported as EventLevelUnconfirmed if it still fails.
// Only the session ending triggers a reconnect.
func (s *Supervisor) serve(ctx context.Context, sess Session, fwd *sync.WaitGroup, resetBackoff func()) bool {
	s.mu.Lock()
	if s.closed {
		floor := s.floor
		s.mu.Unlock()
		s.closeWithFloor(sess, floor)
		return false
	}
	s.sess = sess
	max := sess.MaxLevel()
	if s.o.MaxLevel > 0 && s.o.MaxLevel < max {
		max = s.o.MaxLevel
	}
	s.caps = Caps{Protocol: sess.Protocol(), MinLevel: ClampLevel(s.floor, s.floor, max), MaxLevel: max}
	s.target = ClampLevel(s.target, s.floor, max)
	target := s.target
	first := target
	if s.forceLevel > 0 { // a watchdog trip while disconnected: floor first
		first = ClampLevel(s.forceLevel, s.floor, max)
		s.forceLevel = 0
	}
	s.mu.Unlock()

	obs := make(chan Telemetry, supObsBuf)
	fctx, stopFwd := context.WithCancel(ctx)
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		s.forward(fctx, sess, obs)
	}()
	// end waits for the forwarder so telemetry stays ordered across
	// sessions. It is cancelled only on shutdown; otherwise it flushes what
	// the ended session buffered.
	end := func(cancelled bool) {
		if cancelled {
			stopFwd()
		}
		fwd.Wait()
		stopFwd()
	}
	// fail handles a non-ok write result; it reports whether serve is over
	// and what to return.
	fail := func(r writeRes, err error) bool {
		switch r {
		case wCancelled:
			end(true)
		case wEnded:
			s.drop(sess, err, end)
		}
		return r != wOK
	}

	l := &link{s: s, ctx: ctx, sess: sess, obs: obs, reset: resetBackoff}
	gate, confT := time.NewTimer(time.Hour), time.NewTimer(time.Hour)
	gate.Stop()
	confT.Stop()
	defer gate.Stop()
	defer confT.Stop()

	// The first write on every (re)connect re-asserts the target, bypassing
	// the session's dedup and the rate limit.
	l.handled = first
	if r, err := l.write(first, true, 1); fail(r, err) {
		return r == wEnded
	}
	s.mu.Lock()
	if s.sess == sess { // not closed concurrently
		s.caps.Connected = true
	}
	s.mu.Unlock()
	s.emit(Event{Kind: EventConnected})
	for {
		if r, err := l.act(); fail(r, err) {
			return r == wEnded
		}
		l.arm(gate, confT)
		select {
		case <-ctx.Done():
			end(true)
			return false
		case <-sess.Done():
			if ctx.Err() != nil {
				end(true)
				return false
			}
			return s.drop(sess, sess.Err(), end)
		case t := <-obs:
			l.sample(t)
		case <-s.kick:
		case <-gate.C:
		case <-confT.C:
		}
	}
}

type writeRes int

const (
	wOK        writeRes = iota // written, or failed on a live link (retry scheduled)
	wCancelled                 // ctx cancelled
	wEnded                     // the session ended
)

// confirm tracks one written level awaiting a matching sample.
type confirm struct {
	active   bool
	want     int
	attempt  int // 1 = first write, 2 = the Resend retry
	deadline time.Time
	err      error // write error that started it, if any
}

// link is the per-session write state owned by the serve loop.
type link struct {
	s     *Supervisor
	ctx   context.Context
	sess  Session
	obs   <-chan Telemetry
	reset func() // backoff reset

	handled   int       // last target we wrote or gave up on
	lastWrite time.Time // any write, retries included
	seen      bool      // this session reported Level > 0
	reported  int       // last reported Level > 0
	conf      confirm
	gateWait  time.Duration // >0: a target waits this long for the rate limit
}

// sample folds one telemetry sample into the confirmation state.
func (l *link) sample(t Telemetry) {
	if t.Level <= 0 {
		return
	}
	l.seen = true
	l.reported = t.Level
	if l.conf.active && t.Level == l.conf.want {
		l.conf = confirm{}
	}
}

// drain applies queued samples so a stale one cannot confirm a new write.
func (l *link) drain() {
	for {
		select {
		case t := <-l.obs:
			l.sample(t)
		default:
			return
		}
	}
}

// write sends level (Resend always writes, SetLevel dedups) and starts
// confirmation. attempt 2 is the single retry: its failure is final.
func (l *link) write(level int, resend bool, attempt int) (writeRes, error) {
	l.drain()
	var got int
	var err error
	if resend {
		got, err = l.sess.Resend(l.ctx, level)
	} else {
		got, err = l.sess.SetLevel(l.ctx, level)
	}
	now := time.Now()
	l.lastWrite = now
	l.conf = confirm{}
	// The retry is a write: never schedule it inside the rate-limit gate.
	deadline := now.Add(max(l.s.o.ConfirmWindow, l.s.o.MinWriteInterval))
	switch {
	case err == nil:
		l.reset()
		if l.seen {
			l.conf = confirm{active: true, want: got, attempt: attempt, deadline: deadline}
		}
	case l.ctx.Err() != nil:
		return wCancelled, err
	case ended(l.sess):
		return wEnded, err
	case attempt >= 2:
		l.s.emit(Event{Kind: EventLevelUnconfirmed, Want: level, Got: l.reported, Err: err})
	default:
		l.conf = confirm{active: true, want: level, attempt: attempt, deadline: deadline, err: err}
	}
	return wOK, nil
}

// act performs whatever is due: the confirmation retry or alert, a forced
// watchdog write, or the pending target once the rate limit allows it.
func (l *link) act() (writeRes, error) {
	l.gateWait = 0
	if c := l.conf; c.active && !time.Now().Before(c.deadline) {
		if c.attempt == 1 {
			if r, err := l.write(c.want, true, 2); r != wOK {
				return r, err
			}
		} else {
			l.conf = confirm{}
			l.s.emit(Event{Kind: EventLevelUnconfirmed, Want: c.want, Got: l.reported, Err: c.err})
		}
	}
	l.s.mu.Lock()
	target, force := l.s.target, l.s.forceLevel
	l.s.forceLevel = 0
	l.s.mu.Unlock()
	switch {
	case force > 0:
		// Exactly the forced level; a newer target follows through the gate.
		l.handled = force
		if r, err := l.write(force, true, 1); r != wOK || target == force {
			return r, err
		}
		fallthrough
	case target != l.handled:
		if wait := l.lastWrite.Add(l.s.o.MinWriteInterval).Sub(time.Now()); wait > 0 {
			l.gateWait = wait
			return wOK, nil
		}
		l.handled = target
		return l.write(target, false, 1)
	}
	return wOK, nil
}

// arm schedules the next wake-up for the rate-limit gate and confirmation.
func (l *link) arm(gate, confT *time.Timer) {
	if l.gateWait > 0 {
		gate.Reset(l.gateWait)
	} else {
		gate.Stop()
	}
	if l.conf.active {
		confT.Reset(max(0, time.Until(l.conf.deadline)))
	} else {
		confT.Stop()
	}
}

// tripWatchdog retargets the floor and forces its write. It reports false
// when the supervisor is closed.
func (s *Supervisor) tripWatchdog() bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false
	}
	s.target = s.caps.MinLevel
	s.forceLevel = s.caps.MinLevel
	s.mu.Unlock()
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return true
}

// watchdog trips when no sample with Cadence > 0 arrived for WatchdogTimeout
// (also while disconnected): it retargets the floor, forces the write and
// emits EventWatchdog once, then stays disarmed until the next such sample.
func (s *Supervisor) watchdog(ctx context.Context) {
	defer s.bg.Done()
	t := time.NewTimer(s.o.WatchdogTimeout)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wdArm:
			t.Reset(s.o.WatchdogTimeout)
		case <-t.C:
			select { // a sample racing the expiry wins
			case <-s.wdArm:
				t.Reset(s.o.WatchdogTimeout)
				continue
			default:
			}
			if !s.tripWatchdog() {
				return
			}
			s.emit(Event{Kind: EventWatchdog})
		}
	}
}

func ended(sess Session) bool {
	select {
	case <-sess.Done():
		return true
	default:
		return false
	}
}

// drop releases a session that ended and reports it. It returns true: the
// caller should redial.
func (s *Supervisor) drop(sess Session, err error, end func(bool)) bool {
	s.mu.Lock()
	owned := s.sess == sess
	if owned {
		s.sess = nil
	}
	s.caps.Connected = false
	s.mu.Unlock()
	if owned {
		closeBounded(sess)
	}
	end(false)
	s.emit(Event{Kind: EventDisconnected, Err: err})
	return true
}

// forward copies the session's telemetry to the persistent channel and to
// the serve loop (obs) until the session ends or ctx is cancelled. Samples
// with Cadence > 0 re-arm the watchdog.
func (s *Supervisor) forward(ctx context.Context, sess Session, obs chan Telemetry) {
	tel := sess.Telemetry()
	deliver := func(t Telemetry) {
		if t.Cadence > 0 {
			select {
			case s.wdArm <- struct{}{}:
			default:
			}
		}
		push(s.tel, t)
		push(obs, t)
	}
	for {
		select {
		case t, ok := <-tel:
			if !ok {
				return
			}
			deliver(t)
		case <-sess.Done():
			for { // flush what the session buffered before ending
				select {
				case t, ok := <-tel:
					if !ok {
						return
					}
					deliver(t)
				default:
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
