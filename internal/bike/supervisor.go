package bike

import (
	"context"
	"sync"
	"time"
)

const (
	supTelemetryBuf = 16
	supEventBuf     = 32
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

	mu      sync.Mutex
	target  int
	caps    Caps
	sess    Session // current session; closed by whoever clears it
	started bool
	closed  bool
	cancel  context.CancelFunc
	exited  chan struct{} // closed when run (and the channels) are finished

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
		target: ClampLevel(floor, floor, max),
		caps:   Caps{MinLevel: ClampLevel(floor, floor, max), MaxLevel: max},
		exited: make(chan struct{}),
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
	go s.run(rctx)
	return nil
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

// Close stops supervision, sends the floor level to a connected session and
// closes Telemetry and Events. It is idempotent.
func (s *Supervisor) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		started := s.started
		s.mu.Unlock()
		if started {
			<-s.exited
		}
		return nil
	}
	s.closed = true
	started := s.started
	sess := s.sess
	s.sess = nil
	s.caps.Connected = false
	cancel := s.cancel
	s.mu.Unlock()

	if !started {
		close(s.tel)
		close(s.ev)
		return nil
	}
	cancel()
	var err error
	if sess != nil {
		err = sess.Close(ctx)
	}
	<-s.exited
	return err
}

// push sends v, dropping the oldest buffered value when ch is full. Each
// channel has a single producer at a time.
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
		sess := s.sess
		s.sess = nil
		s.caps.Connected = false
		s.mu.Unlock()
		if sess != nil {
			closeBounded(sess)
		}
		fwd.Wait()
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
				closeBounded(sess)
			}
			return
		}
		if err != nil {
			s.emit(Event{Kind: EventDisconnected, Err: err})
			wait = nextWait()
			continue
		}
		backoff = s.o.BackoffMin
		if !s.serve(ctx, sess, &fwd) {
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

// serve owns one connected session until it ends. It returns false when ctx
// was cancelled (supervision is over).
func (s *Supervisor) serve(ctx context.Context, sess Session, fwd *sync.WaitGroup) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closeBounded(sess)
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
	s.mu.Unlock()

	fctx, stopFwd := context.WithCancel(ctx)
	fwd.Add(1)
	go func() {
		defer fwd.Done()
		s.forward(fctx, sess)
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

	// The first write on every (re)connect re-asserts the target, bypassing
	// the session's dedup.
	last, err := sess.Resend(ctx, target)
	if err == nil {
		s.mu.Lock()
		if s.sess == sess { // not closed concurrently
			s.caps.Connected = true
		}
		s.mu.Unlock()
		s.emit(Event{Kind: EventConnected})
		for {
			select {
			case <-ctx.Done():
				end(true)
				return false
			case <-sess.Done():
				err = sess.Err()
			case <-s.kick:
				s.mu.Lock()
				target = s.target
				s.mu.Unlock()
				if target == last {
					continue
				}
				if last, err = sess.SetLevel(ctx, target); err == nil {
					continue
				}
			}
			break
		}
	}
	if ctx.Err() != nil {
		end(true)
		return false
	}

	// The session failed or ended: release it and report.
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

// forward copies the session's telemetry to the persistent channel until the
// session ends or ctx is cancelled.
func (s *Supervisor) forward(ctx context.Context, sess Session) {
	tel := sess.Telemetry()
	for {
		select {
		case t, ok := <-tel:
			if !ok {
				return
			}
			push(s.tel, t)
		case <-sess.Done():
			for { // flush what the session buffered before ending
				select {
				case t, ok := <-tel:
					if !ok {
						return
					}
					push(s.tel, t)
				default:
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
