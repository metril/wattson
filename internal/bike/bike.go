package bike

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/metril/wattson/internal/blue"
)

var (
	// ErrUnsupported means the device does not expose the requested/any known protocol.
	ErrUnsupported = errors.New("bike: no supported protocol on device")
	// ErrClosed means the session was closed.
	ErrClosed = errors.New("bike: session closed")
	// ErrSilence is wrapped (with blue.ErrDisconnected) in Bike.Err when the
	// silence watchdog ended the session.
	ErrSilence = errors.New("bike: telemetry silence")
	// ErrControlRejected means the FTMS control point answered with a non-success result.
	ErrControlRejected = errors.New("bike: control point rejected request")
)

const (
	defaultKeepalive      = 200 * time.Millisecond
	defaultControlTimeout = time.Second
	defaultWriteTimeout   = 2 * time.Second
	defaultMaxLevel       = 16 // S29M cap; also the FitPlus default (never above 32)
	telemetryBuf          = 16
	ftmsU8MaxLevel        = 25
	ftmsResultSuccess     = 0x01
	ftmsOpSetResistance   = 0x04
)

// Options configures Open. Zero values select defaults.
type Options struct {
	Protocol       Protocol // ProtoUnknown = auto-detect
	MinLevel       int      // floored at 1
	MaxLevel       int      // 0 = device range (FTMS) or 16 (otherwise); capped by the device
	FTMSEnc        ResistanceEncoding
	Keepalive      time.Duration    // FitPlus keepalive period; default 200ms
	Now            func() time.Time // Telemetry.At clock; default time.Now
	ControlTimeout time.Duration    // FTMS: wait for a control response to SetLevel; default 1s
	WriteTimeout   time.Duration    // bound on every post-Open write; default 2s
	// SilenceTimeout, when > 0, is a transport-agnostic liveness watchdog:
	// armed by the first telemetry frame, it treats the link as down when no
	// telemetry arrives for this long (see Bike.Done). 0 disables it.
	SilenceTimeout time.Duration

	silenceTimer func(d time.Duration) silenceTimer // test hook; default: time.Timer
}

// silenceTimer is the watchdog's timer, replaceable in tests.
type silenceTimer interface {
	C() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time   { return r.t.C }
func (r realTimer) Reset(d time.Duration) { r.t.Reset(d) }
func (r realTimer) Stop()                 { r.t.Stop() }

// Bike is a live session with one bike. It is safe for concurrent use. It has
// no way to send a reset or level 0.
type Bike struct {
	conn  blue.Conn
	proto Protocol
	min   int
	max   int
	enc   ResistanceEncoding
	now   func() time.Time
	ka    time.Duration
	ctlTO time.Duration
	wt    time.Duration

	writeCh   blue.UUID
	writeResp bool

	ctx    context.Context
	cancel context.CancelFunc
	exited chan struct{}

	mu     sync.Mutex // serialises writes and guards closed/last
	closed bool
	last   int // last level sent; 0 = none

	ctl chan ControlResponse // FTMS control point indications

	tmu     sync.Mutex
	tel     chan Telemetry
	telDone bool

	silence time.Duration
	newTmr  func(d time.Duration) silenceTimer
	telSig  chan struct{} // a frame arrived (re-arms the watchdog)
	done    chan struct{} // closed when the session ends: link drop, silence or Close
	endOnce sync.Once
	emu     sync.Mutex
	endErr  error // why the session ended; nil for a normal Close
	watchWG sync.WaitGroup
}

// Open detects the protocol, subscribes, performs the init handshake and
// starts the session goroutine. The FTMS Request Control result is not checked
// (control responses are ignored at Open); a rejected SetLevel surfaces later.
// On error the caller still owns c. After a
// successful Open, Close releases c.
func Open(ctx context.Context, c blue.Conn, o Options) (*Bike, error) {
	proto := o.Protocol
	svcs := c.Services()
	switch proto {
	case ProtoUnknown:
		proto = Detect(svcs)
	case ProtoFitPlus:
		if !hasService(svcs, FitPlusService, FitPlusNotify, FitPlusWrite) {
			proto = ProtoUnknown
		}
	case ProtoFTMS:
		if !hasService(svcs, FTMSService, FTMSControlPoint) {
			proto = ProtoUnknown
		}
	default:
		proto = ProtoUnknown
	}
	if proto == ProtoUnknown {
		return nil, fmt.Errorf("%w (requested %s)", ErrUnsupported, o.Protocol)
	}

	b := &Bike{
		conn: c, proto: proto, enc: o.FTMSEnc, now: o.Now, ka: o.Keepalive, ctlTO: o.ControlTimeout,
		min: max(1, o.MinLevel),
		tel: make(chan Telemetry, telemetryBuf),
		ctl: make(chan ControlResponse, 8),

		silence: o.SilenceTimeout,
		newTmr:  o.silenceTimer,
		telSig:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.ka <= 0 {
		b.ka = defaultKeepalive
	}
	if b.ctlTO <= 0 {
		b.ctlTO = defaultControlTimeout
	}
	b.wt = o.WriteTimeout
	if b.wt <= 0 {
		b.wt = defaultWriteTimeout
	}
	if b.newTmr == nil {
		b.newTmr = func(d time.Duration) silenceTimer { return realTimer{time.NewTimer(d)} }
	}
	b.ctx, b.cancel = context.WithCancel(context.Background())

	var err error
	switch proto {
	case ProtoFitPlus:
		err = b.openFitPlus(ctx, o)
	case ProtoFTMS:
		err = b.openFTMS(ctx, o)
	}
	if err != nil {
		b.cancel()
		return nil, err
	}
	b.min = min(b.min, b.max)
	b.exited = make(chan struct{})
	go b.run()
	b.watchWG.Add(1)
	go b.watch()
	return b, nil
}

func (b *Bike) openFitPlus(ctx context.Context, o Options) error {
	b.max = defaultMaxLevel
	if o.MaxLevel > 0 {
		b.max = min(o.MaxLevel, len(fitPlusLevels))
	}
	b.writeCh = FitPlusWrite
	b.writeResp = !b.charHas(FitPlusWrite, blue.PropWriteNoResp)
	if err := b.conn.Subscribe(ctx, FitPlusNotify, func(p []byte) {
		if t, ok := ParseFitPlus(p); ok {
			b.publish(t)
		}
	}); err != nil {
		return fmt.Errorf("bike: subscribe fitplus: %w", err)
	}
	if err := b.conn.Write(ctx, FitPlusWrite, FitPlusInit(), b.writeResp); err != nil {
		return fmt.Errorf("bike: fitplus init: %w", err)
	}
	return nil
}

func (b *Bike) openFTMS(ctx context.Context, o Options) error {
	b.writeCh, b.writeResp = FTMSControlPoint, true
	if err := b.conn.Subscribe(ctx, FTMSIndoorBikeData, func(p []byte) {
		if t, err := ParseIndoorBikeData(p); err == nil {
			b.publish(t)
		}
	}); err != nil {
		return fmt.Errorf("bike: subscribe indoor bike data: %w", err)
	}
	if err := b.conn.Subscribe(ctx, FTMSControlPoint, func(p []byte) {
		if r, err := ParseControlResponse(p); err == nil {
			select {
			case b.ctl <- r:
			default: // nobody is waiting; drop
			}
		}
	}); err != nil {
		return fmt.Errorf("bike: subscribe control point: %w", err)
	}
	if err := b.conn.Write(ctx, FTMSControlPoint, FTMSRequestControl(), true); err != nil {
		return fmt.Errorf("bike: request control: %w", err)
	}

	rangeMax := 0
	if v, err := b.conn.Read(ctx, FTMSResistanceRange); err == nil {
		if _, mx, _, perr := ParseResistanceRange(v); perr == nil && mx >= 1 {
			rangeMax = int(math.Floor(mx + 1e-9))
		}
	} else if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case rangeMax > 0:
		b.max = rangeMax
		if o.MaxLevel > 0 {
			b.max = min(b.max, o.MaxLevel)
		}
		if rangeMax > ftmsU8MaxLevel {
			b.enc = EncU16Tenths
		}
	case o.MaxLevel > 0:
		b.max = o.MaxLevel
	default:
		b.max = defaultMaxLevel
	}
	return nil
}

func (b *Bike) charHas(ch blue.UUID, p blue.Props) bool {
	for _, s := range b.conn.Services() {
		for _, x := range s.Chars {
			if x.UUID == ch {
				return x.Props&p != 0
			}
		}
	}
	return false
}

// publish stamps and enqueues a sample, dropping the oldest when full.
func (b *Bike) publish(t Telemetry) {
	t.At = b.now()
	select { // re-arm the silence watchdog
	case b.telSig <- struct{}{}:
	default:
	}
	b.tmu.Lock()
	defer b.tmu.Unlock()
	if b.telDone {
		return
	}
	for {
		select {
		case b.tel <- t:
			return
		default:
		}
		select {
		case <-b.tel:
		default:
		}
	}
}

func (b *Bike) closeTelemetry() {
	b.tmu.Lock()
	defer b.tmu.Unlock()
	if !b.telDone {
		b.telDone = true
		close(b.tel)
	}
}

// run is the only goroutine: FitPlus keepalive plus exit on Close or link loss.
func (b *Bike) run() {
	defer close(b.exited)
	defer b.closeTelemetry()
	var tick <-chan time.Time
	if b.proto == ProtoFitPlus {
		t := time.NewTicker(b.ka)
		defer t.Stop()
		tick = t.C
	}
	for i := 0; ; {
		select {
		case <-b.ctx.Done():
			return
		case <-b.conn.Done():
			return
		case <-tick:
			b.keepalive(i)
			i++
		}
	}
}

// watch ends the session when the link drops or, if enabled, telemetry stops.
// It runs apart from run so a keepalive write stuck on a dead link cannot
// delay detection.
func (b *Bike) watch() {
	defer b.watchWG.Done()
	var timer silenceTimer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-b.conn.Done():
			b.end(b.downErr())
			return
		case <-b.telSig:
			if b.silence <= 0 {
				continue
			}
			if timer == nil {
				timer = b.newTmr(b.silence)
				fire = timer.C()
			} else {
				timer.Reset(b.silence)
			}
		case <-fire:
			b.end(fmt.Errorf("no telemetry for %s: %w: %w", b.silence, ErrSilence, blue.ErrDisconnected))
			b.closeTelemetry()
			return
		}
	}
}

// end marks the session over (first call wins) and wakes Done.
func (b *Bike) end(err error) {
	b.endOnce.Do(func() {
		b.emu.Lock()
		b.endErr = err
		b.emu.Unlock()
		close(b.done)
	})
}

// Done is closed when the session ends: the connection dropped, the silence
// watchdog fired, or Close was called.
func (b *Bike) Done() <-chan struct{} { return b.done }

// Err is why the session ended: nil while it runs or after a normal Close,
// otherwise an error wrapping blue.ErrDisconnected (link drop or silence).
func (b *Bike) Err() error {
	b.emu.Lock()
	defer b.emu.Unlock()
	return b.endErr
}

// downed reports a link failure (drop or silence) recorded by the session.
func (b *Bike) downed() error {
	select {
	case <-b.done:
		return b.Err()
	default:
		return nil
	}
}

func (b *Bike) keepalive(i int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.linkDown() || b.downed() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, b.wt)
	defer cancel()
	_ = b.conn.Write(ctx, FitPlusWrite, FitPlusKeepalive(i), b.writeResp)
}

func (b *Bike) linkDown() bool {
	select {
	case <-b.conn.Done():
		return true
	default:
		return false
	}
}

func (b *Bike) downErr() error {
	if err := b.conn.Err(); err != nil {
		return err
	}
	return blue.ErrDisconnected
}

func (b *Bike) Protocol() Protocol { return b.proto }

// MaxLevel is the highest level SetLevel will send.
func (b *Bike) MaxLevel() int { return b.max }

// Telemetry delivers samples (buffer 16, oldest dropped when full). It is
// closed when the session ends (Close or link loss).
func (b *Bike) Telemetry() <-chan Telemetry { return b.tel }

// SetLevel clamps level into [MinLevel, MaxLevel] (never below 1) and sends it
// unless it equals the last level sent. It returns the clamped level.
func (b *Bike) SetLevel(ctx context.Context, level int) (sent int, err error) {
	return b.set(ctx, level, false)
}

// Resend is SetLevel without the dedup: the clamped level is always written.
// The driver uses it to re-assert a level after a suspected silent drop.
func (b *Bike) Resend(ctx context.Context, level int) (sent int, err error) {
	return b.set(ctx, level, true)
}

func (b *Bike) set(ctx context.Context, level int, force bool) (sent int, err error) {
	// Abort promptly when Close cancels the session, even mid-write.
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(b.ctx, stop)()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.ctx.Err() != nil {
		return 0, ErrClosed
	}
	if b.linkDown() {
		return 0, b.downErr()
	}
	if err := b.downed(); err != nil {
		return 0, err
	}
	level = ClampLevel(level, b.min, b.max)
	if !force && level == b.last {
		return level, nil
	}
	if err := b.sendLevel(ctx, level); err != nil {
		return 0, err
	}
	return level, nil
}

// sendLevel writes a clamped level; b.mu must be held. last is invalidated
// before the write: on any failure the bike may or may not have applied the
// frame, so the next request for any level must be sent. The write itself is
// bounded by WriteTimeout.
func (b *Bike) sendLevel(ctx context.Context, level int) error {
	var frame []byte
	var err error
	if b.proto == ProtoFitPlus {
		frame, err = FitPlusSetLevel(level)
	} else {
		frame, err = FTMSSetResistance(level, b.enc)
	}
	if err != nil {
		return err
	}
	if b.proto == ProtoFTMS {
		for len(b.ctl) > 0 { // discard stale responses
			<-b.ctl
		}
	}
	b.last = 0
	wctx, cancel := context.WithTimeout(ctx, b.wt)
	err = b.conn.Write(wctx, b.writeCh, frame, b.writeResp)
	cancel()
	if err != nil {
		return err
	}
	if b.proto == ProtoFTMS {
		if err := b.awaitResponse(ctx); err != nil {
			return err
		}
	}
	b.last = level
	return nil
}

// awaitResponse waits up to ctlTO for the Set Target Resistance response. No
// response is not an error; a non-success result is. Known limit: a response
// arriving after the wait is discarded as stale by the next SetLevel, so a
// late rejection goes unnoticed.
func (b *Bike) awaitResponse(ctx context.Context) error {
	timer := time.NewTimer(b.ctlTO)
	defer timer.Stop()
	for {
		select {
		case r := <-b.ctl:
			if r.Op != ftmsOpSetResistance {
				continue
			}
			if r.Result != ftmsResultSuccess {
				return fmt.Errorf("%w: result %#02x", ErrControlRejected, r.Result)
			}
			return nil
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-b.conn.Done():
			return b.downErr()
		}
	}
}

// Close sends the minimum level (never 0) while the link is up, stops the
// session goroutine and closes the connection. It is idempotent. If the link
// already dropped nothing is written and the result is nil.
func (b *Bike) Close(ctx context.Context) error {
	// Cancel first so a write stuck under mu (e.g. a dead link) is aborted
	// instead of making Close queue behind it.
	b.cancel()
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.end(nil) // no-op if a drop or silence already ended the session
	var werr error
	if !b.linkDown() {
		werr = b.sendLevel(ctx, ClampLevel(b.min, b.min, b.max))
	}
	b.mu.Unlock()
	<-b.exited
	b.watchWG.Wait()
	return errors.Join(werr, b.conn.Close())
}
