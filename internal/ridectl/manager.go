// Package ridectl owns the one bike a Wattson server controls: it connects a
// rider to the bike (or simulator), runs one ride engine at a time, fans
// status and ride ticks out to subscribers and scans for bikes. The REST and
// WebSocket layers drive it.
package ridectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/bike/sim"
	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/hass"
	"github.com/metril/wattson/internal/route"
	"github.com/metril/wattson/internal/sensors"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/workout"
)

var (
	ErrBusy      = errors.New("ridectl: a bike is already connected or a scan is running")
	ErrRiding    = errors.New("ridectl: a ride is in progress")
	ErrNoBike    = errors.New("ridectl: no bike connected")
	ErrNoAddress = errors.New("ridectl: no bike address configured")
	ErrNoHost    = errors.New("ridectl: no ESPHome host configured")
	ErrClosed    = errors.New("ridectl: manager closed")

	// ErrNoHAURL and ErrNoHAToken mean Home Assistant is needed (as the bike or
	// the heart rate source) but not configured. Dial errors wrap them with
	// bike.ErrConfig.
	ErrNoHAURL   = errors.New("ridectl: no Home Assistant URL configured")
	ErrNoHAToken = errors.New("ridectl: no Home Assistant token configured")

	// ErrRouteNotFound is returned by StartRoute (and a Deps.LoadRoute) for a route that does not exist.
	ErrRouteNotFound = errors.New("ridectl: route not found")

	// ErrWorkoutNotFound is returned by StartWorkout (and a Deps.LoadWorkout) for a workout that does not exist or is no longer valid.
	ErrWorkoutNotFound = errors.New("ridectl: workout not found")
)

// Deps are the manager's collaborators; every func has a production default.
type Deps struct {
	Settings     func(context.Context) (settings.Settings, error)
	OpenLocal    func() (blue.Transport, error)                                                                     // default blue.NewLocal
	OpenESPHome  func(ctx context.Context, hostport, key string) (blue.Transport, error)                            // default blue.NewESPHome
	ProbeESPHome func(ctx context.Context, hostport, key string, slotsWait time.Duration) (blue.ProxyStatus, error) // default blue.ProbeESPHome; diagnostics only, never subscribes
	SimDial      func(sim.Config) bike.Dial                                                                         // default sim.Dial
	LoadRoute    func(ctx context.Context, id int64) (*route.Profile, error)                                        // route for StartRoute; ErrRouteNotFound when missing; see StoreRoutes
	LoadWorkout  func(ctx context.Context, id int64) (store.Workout, error)                                         // workout for StartWorkout; ErrWorkoutNotFound when missing; see StoreWorkouts
	PowerModels  PowerModelStore                                                                                    // persists the learned power model per bike; nil keeps it in memory only
	TickEvery    time.Duration                                                                                      // engine tick period; default 1s
	SimPeriod    time.Duration                                                                                      // simulator telemetry period; default 1s
	OpenHA       func(ctx context.Context, base, token string) (*hass.Client, error)                                // default hass.Dial(Options{})
	HAPeriod     time.Duration                                                                                      // Home Assistant bike telemetry period; default 1s
	HRBackoffMin time.Duration                                                                                      // first heart rate reconnect delay; default 1s
	Logger       *slog.Logger
}

// Status is the bike connection as the UI shows it.
type Status struct {
	State            string    `json:"state" doc:"idle when no bike is connected, otherwise the engine state: connecting, ready, riding, paused or finished."`
	RiderID          int64     `json:"rider_id,omitempty" doc:"Rider bound to the bike; absent when idle."`
	Driver           string    `json:"driver,omitempty" doc:"Driver from settings in use: auto, fitplus, ftms, ha or sim."`
	Protocol         string    `json:"protocol,omitempty" doc:"Protocol of the live bike session: fitplus, ftms, ha or sim."`
	Connected        bool      `json:"connected" doc:"Whether a live link to the bike exists right now."`
	LastError        string    `json:"last_error,omitempty" doc:"Most recent problem: a failed connection, an idle release, or a bike that could not be confirmed back at its minimum level."`
	Hint             string    `json:"hint,omitempty" doc:"Advice for fixing LastError when it is a connection problem."`
	FloorUnconfirmed bool      `json:"floor_unconfirmed" doc:"True while the bike could not be confirmed back at its minimum level, so the rider should lower the resistance at the console; clears once a floor write is confirmed."`
	HR               *HRInfo   `json:"hr,omitempty" doc:"Heart rate source state; absent when heart_rate.source is off or its settings are unusable. The live reading is heart_rate_bpm in each tick."`
	Since            time.Time `json:"since" doc:"When State last changed (RFC 3339)."`
}

// statusEqual compares statuses (HR is a pointer).
func statusEqual(a, b Status) bool {
	if (a.HR == nil) != (b.HR == nil) || (a.HR != nil && *a.HR != *b.HR) {
		return false
	}
	a.HR, b.HR = nil, nil
	return a == b
}

// HRInfo is the heart rate source as the status shows it. The live reading is
// not part of it: it changes every second and travels in the ticks.
type HRInfo struct {
	Source    string `json:"source" doc:"Heart rate source: ble (Bluetooth monitor) or ha (Home Assistant entity)."`
	Target    string `json:"target" doc:"What the source reads: a Bluetooth address with optional monitor name, or a Home Assistant entity id."`
	Connected bool   `json:"connected" doc:"True while the source is delivering or able to deliver readings."`
	Error     string `json:"error,omitempty" doc:"Last error from the source; absent when it is healthy."`
}

// conn is one bike connection: a rider, one transport, and the sequence of
// engines run over it (an engine is single-use, so a stopped ride is followed
// by a fresh engine for the same rider).
type conn struct {
	rider  store.Rider
	plan   plan
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}  // closed after the connection is fully torn down
	tr     blue.Transport // shared Bluetooth transport; nil when nothing uses Bluetooth

	// The learned power model of this bike: loaded at connect, shared by every
	// engine of the connection and saved whenever an engine's Run returns.
	key    string
	model  *bike.PowerModel
	savedN int // sample count already stored; guarded by Manager.pmMu

	// Heart rate source, set by startHR before the first engine and stopped by finish.
	hrMon    sensors.Monitor
	hrCancel context.CancelFunc
	hrDone   chan struct{}
	finished bool // finish has torn the connection down; guarded by Manager.mu

	// Guarded by Manager.mu.
	eng        *engine.Engine
	tap        *tap
	state      engine.State
	connected  bool
	proto      string
	releasing  bool
	relErr     error // floor outcome of the final engine; read after done
	lastActive time.Time
	connErr    string
	hint       string
	note       string // why the connection was released (idle timeout)
	floorWarn  string // the bike was not confirmed back at its minimum
}

func (c *conn) lastError() string {
	switch {
	case c.floorWarn != "":
		return c.floorWarn
	case c.note != "":
		return c.note
	}
	return c.connErr
}

// Manager serialises access to one bike. Safe for concurrent use.
type Manager struct {
	d    Deps
	log  *slog.Logger
	hub  *hub
	root context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	pmMu     sync.Mutex // serialises power model saves and resets (store I/O under it; never held with mu while waiting)
	mu       sync.Mutex
	cur      *conn
	status   Status
	scanning bool

	diagProxy time.Duration // ProxyStatus bound for diagnostics; zero means diagProxyBound (tests shorten it)
	closed    bool
}

// New returns an idle Manager.
func New(d Deps) *Manager {
	if d.Settings == nil {
		d.Settings = func(context.Context) (settings.Settings, error) { return settings.Defaults(), nil }
	}
	if d.OpenLocal == nil {
		d.OpenLocal = blue.NewLocal
	}
	if d.ProbeESPHome == nil {
		d.ProbeESPHome = blue.ProbeESPHome
	}
	if d.OpenESPHome == nil {
		d.OpenESPHome = blue.NewESPHome
	}
	if d.SimDial == nil {
		d.SimDial = sim.Dial
	}
	if d.OpenHA == nil {
		d.OpenHA = func(ctx context.Context, base, token string) (*hass.Client, error) {
			return hass.Dial(ctx, base, token, hass.Options{})
		}
	}
	if d.HAPeriod <= 0 {
		d.HAPeriod = time.Second
	}
	if d.HRBackoffMin <= 0 {
		d.HRBackoffMin = time.Second
	}
	if d.TickEvery <= 0 {
		d.TickEvery = time.Second
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	root, stop := context.WithCancel(context.Background())
	return &Manager{d: d, log: d.Logger, hub: newHub(), root: root, stop: stop,
		status: Status{State: string(engine.StateIdle), Since: time.Now()}}
}

// Connect binds rider r to the bike and returns at once with state
// "connecting"; progress and failures arrive on Subscribe. ErrBusy unless idle.
func (m *Manager) Connect(ctx context.Context, r store.Rider) (Status, error) {
	m.mu.Lock()
	busy := m.cur != nil || m.scanning
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return Status{}, ErrClosed
	}
	if busy {
		return Status{}, ErrBusy
	}
	s, err := m.d.Settings(ctx)
	if err != nil {
		return Status{}, err
	}
	p, err := planFor(s)
	if err != nil {
		return Status{}, err
	}
	if p.hrErr != nil { // heart rate is optional: ride without it
		m.log.Warn("heart rate disabled", "err", p.hrErr)
	}
	r.MinLevel = max(r.MinLevel, 1)
	r.MaxLevel = max(r.MaxLevel, r.MinLevel)

	key := BikeKey(s)
	// pmMu spans the load and the install of the connection, so an idle
	// ResetPowerModel cannot slip between them and leave c with a stale
	// model (it would find c installed and reset it in place instead).
	m.pmMu.Lock()
	defer m.pmMu.Unlock()
	model, savedN := m.loadModel(ctx, key)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Status{}, ErrClosed
	}
	if m.cur != nil || m.scanning {
		return Status{}, ErrBusy
	}
	cctx, cancel := context.WithCancel(m.root)
	c := &conn{rider: r, plan: p, ctx: cctx, cancel: cancel, done: make(chan struct{}), tr: m.sharedFor(p),
		key: key, model: model, savedN: savedN, state: engine.StateConnecting, lastActive: time.Now()}
	m.cur = c
	m.pushLocked(c)
	m.wg.Add(1)
	go m.run(c)
	if n := s.Bike.IdleReleaseMin; n > 0 {
		m.wg.Add(1)
		go m.idleWatch(c, time.Duration(n)*time.Minute)
	}
	m.log.Info("bike connecting", "rider", r.ID, "driver", p.driver)
	return m.status, nil
}

// Disconnect releases the bike when no ride is under way (ErrRiding while
// riding or paused). It is a no-op when idle.
func (m *Manager) Disconnect(ctx context.Context) error {
	return m.release(ctx, "", true)
}

// Release ends any ride, returns the bike to the rider's minimum level and
// closes the link. It is idempotent. The error says honestly when the bike
// could not be confirmed back at its minimum (the rider must lower resistance
// at the console).
func (m *Manager) Release(ctx context.Context) error {
	return m.release(ctx, "", false)
}

func (m *Manager) release(ctx context.Context, note string, refuseRiding bool) error {
	m.mu.Lock()
	c := m.cur
	if c == nil {
		m.mu.Unlock()
		return nil
	}
	eng := c.eng
	if refuseRiding && eng != nil {
		if st := eng.Snapshot().State; st == engine.StateRiding || st == engine.StatePaused {
			m.mu.Unlock()
			return ErrRiding
		}
	}
	if !c.releasing {
		c.releasing = true
		if note != "" {
			c.note = note
		}
	}
	m.mu.Unlock()

	// A graceful stop floors the bike and ends Run itself. Without a live
	// engine (opening the transport, between engines) cancel the connection.
	if eng == nil {
		c.cancel()
	} else if err := eng.Do(ctx, engine.Command{Kind: engine.CmdStop}); err != nil && !errors.Is(err, engine.ErrStopped) {
		c.cancel()
	}
	select {
	case <-c.done:
		return c.relErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Do sends a ride command to the engine and counts as activity for the idle
// release. ErrNoBike when idle; engine.ErrStopped in the instant between a
// stopped ride and its replacement engine.
func (m *Manager) Do(ctx context.Context, cmd engine.Command) error {
	m.mu.Lock()
	c := m.cur
	if c == nil || c.eng == nil || c.releasing {
		m.mu.Unlock()
		return ErrNoBike
	}
	c.lastActive = time.Now()
	eng := c.eng
	m.mu.Unlock()
	return eng.Do(ctx, cmd)
}

// Status returns the current connection status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// RiderInUse reports whether rider id is bound to the active connection. The
// riders API uses it to refuse deleting that rider (409 rider_in_use).
func (m *Manager) RiderInUse(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur != nil && m.cur.rider.ID == id
}

// RouteInUse reports whether route id is being ridden (riding or paused). The
// routes API uses it to refuse deleting that route (409 route_in_use).
func (m *Manager) RouteInUse(id int64) bool {
	tk := m.Snapshot()
	return id != 0 && tk.RouteID == id && (tk.State == engine.StateRiding || tk.State == engine.StatePaused)
}

// WorkoutInUse reports whether workout id is being ridden (riding or paused).
// The workouts API uses it to refuse deleting that workout (409 workout_in_use).
func (m *Manager) WorkoutInUse(id int64) bool {
	tk := m.Snapshot()
	return id != 0 && tk.WorkoutID == id && (tk.State == engine.StateRiding || tk.State == engine.StatePaused)
}

// target is the connection a start command was issued against.
type target struct {
	c     *conn
	eng   *engine.Engine
	rider store.Rider
	model *bike.PowerModel
}

// startTarget returns the live connection for a start command: ErrNoBike without
// one, engine.ErrBadState unless the bike is ready.
func (m *Manager) startTarget() (target, error) {
	m.mu.Lock()
	c := m.cur
	if c == nil || c.eng == nil || c.releasing {
		m.mu.Unlock()
		return target{}, ErrNoBike
	}
	t := target{c: c, eng: c.eng, rider: c.rider, model: c.model}
	m.mu.Unlock()
	if t.eng.Snapshot().State != engine.StateReady {
		return target{}, engine.ErrBadState
	}
	return t, nil
}

// startMode starts mode on t. The bike may have been released (or swapped for
// a new connection with a possibly different rider) while the start's inputs
// loaded.
func (m *Manager) startMode(ctx context.Context, t target, mode engine.Mode) error {
	m.mu.Lock()
	if m.cur != t.c || t.c.eng != t.eng || t.c.releasing {
		m.mu.Unlock()
		return ErrNoBike
	}
	t.c.lastActive = time.Now()
	m.mu.Unlock()
	return t.eng.Do(ctx, engine.Command{Kind: engine.CmdStart, Mode: mode})
}

// StartRoute starts a ride along the stored route with the rider bound at
// connect (their gain, offset, downhill scale, range and lookahead). ErrNoBike
// without a connection, engine.ErrBadState unless the bike is ready,
// ErrRouteNotFound for an unknown route. The route is read once: deleting it
// mid-ride does not affect the ride.
func (m *Manager) StartRoute(ctx context.Context, routeID int64) error {
	t, err := m.startTarget()
	if err != nil {
		return err
	}
	if m.d.LoadRoute == nil {
		return ErrRouteNotFound
	}
	p, err := m.d.LoadRoute(ctx, routeID)
	if err != nil {
		return err
	}
	r := t.rider
	return m.startMode(ctx, t, engine.NewRouteMode(engine.RouteConfig{
		ID:       routeID,
		Course:   p,
		Mapper:   engine.Mapper{Gain: r.Gain, Offset: r.OffsetLevel, DownhillScale: r.DownhillScale, Min: r.MinLevel, Max: r.MaxLevel},
		Smoother: engine.DefaultSmoother(r.MinLevel, r.MaxLevel),
		LeadS:    r.LeadS,
	}))
}

// StartWorkout starts the stored interval or ERG workout with the rider bound
// at connect: blocks are resolved against the rider once, so editing or
// deleting the workout mid-ride does not affect the ride. ErrNoBike without a
// connection, engine.ErrBadState unless the bike is ready, ErrWorkoutNotFound
// for an unknown workout or one whose stored blocks no longer validate.
func (m *Manager) StartWorkout(ctx context.Context, id int64) error {
	t, err := m.startTarget()
	if err != nil {
		return err
	}
	if m.d.LoadWorkout == nil {
		return ErrWorkoutNotFound
	}
	w, err := m.d.LoadWorkout(ctx, id)
	if err != nil {
		return err
	}
	var bs []workout.Block
	if err := json.Unmarshal(w.Blocks, &bs); err != nil {
		return fmt.Errorf("%w: stored blocks of workout %d: %v", ErrWorkoutNotFound, id, err)
	}
	typ := workout.Type(w.Type)
	if err := workout.Validate(typ, bs); err != nil {
		return fmt.Errorf("%w: workout %d is invalid: %v", ErrWorkoutNotFound, id, err)
	}
	r := t.rider
	blocks := workout.Resolve(typ, bs, r)
	if typ == workout.Erg {
		return m.startMode(ctx, t, engine.NewErgMode(engine.ErgConfig{WorkoutID: id, Blocks: blocks, Model: t.model,
			Multiplier: r.PowerMultiplier, MinLevel: r.MinLevel, MaxLevel: r.MaxLevel}))
	}
	return m.startMode(ctx, t, engine.NewIntervalMode(id, blocks, r.MinLevel, r.MaxLevel))
}

// StartErg starts a free ERG ride holding watts (engine.MinTargetW to
// engine.MaxTargetW; engine.ErrBadValue outside it); change the target with
// the set_target_power command. ErrNoBike without a connection,
// engine.ErrBadState unless the bike is ready.
func (m *Manager) StartErg(ctx context.Context, watts int) error {
	if watts < engine.MinTargetW || watts > engine.MaxTargetW {
		return engine.ErrBadValue
	}
	t, err := m.startTarget()
	if err != nil {
		return err
	}
	r := t.rider
	return m.startMode(ctx, t, engine.NewErgMode(engine.ErgConfig{TargetW: float64(watts), Model: t.model,
		Multiplier: r.PowerMultiplier, MinLevel: r.MinLevel, MaxLevel: r.MaxLevel}))
}

// StoreWorkouts returns a Deps.LoadWorkout that reads workouts from the store.
func StoreWorkouts(st *store.Store) func(context.Context, int64) (store.Workout, error) {
	return func(ctx context.Context, id int64) (store.Workout, error) {
		w, err := st.Workout(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return store.Workout{}, ErrWorkoutNotFound
		}
		return w, err
	}
}

// StoreRoutes returns a Deps.LoadRoute that decodes routes from the store.
func StoreRoutes(st *store.Store) func(context.Context, int64) (*route.Profile, error) {
	return func(ctx context.Context, id int64) (*route.Profile, error) {
		r, err := st.Route(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrRouteNotFound
		}
		if err != nil {
			return nil, err
		}
		p, err := route.Decode(r.Points)
		if err != nil {
			return nil, fmt.Errorf("decode route %d: %w", id, err)
		}
		return p, nil
	}
}

// Snapshot returns the engine's latest tick, or a tick in state "idle".
func (m *Manager) Snapshot() engine.Tick {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() engine.Tick {
	if m.cur == nil || m.cur.eng == nil {
		return engine.Tick{State: engine.StateIdle}
	}
	tk := m.cur.eng.Snapshot()
	if tk.State == engine.StateIdle { // engine built but not started yet
		tk.State = engine.StateConnecting
	}
	return tk
}

// Subscribe returns a channel carrying the current status, then the current
// tick, then every later status and tick. It survives engine swaps. When buf
// is full the oldest event is dropped. Call the cancel func to unsubscribe;
// Close also ends every subscription.
func (m *Manager) Subscribe(buf int) (<-chan Event, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, tk := m.status, m.snapshotLocked()
	return m.hub.add(max(buf, 2), Event{Type: "status", Status: &st}, Event{Type: "tick", Tick: &tk})
}

// Close releases the bike, stops all goroutines and ends every subscription.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	err := m.Release(ctx)
	m.stop()
	m.wg.Wait()
	m.hub.close()
	return err
}

// pushLocked recomputes the status of live connection c and publishes it when
// it changed.
func (m *Manager) pushLocked(c *conn) {
	old := m.status
	st := Status{
		State: string(c.state), RiderID: c.rider.ID, Driver: c.plan.driver, Protocol: c.proto,
		Connected: c.connected, LastError: c.lastError(), Hint: c.hint, FloorUnconfirmed: c.floorWarn != "", HR: c.hrStatus(), Since: old.Since,
	}
	m.setStatusLocked(st)
}

func (m *Manager) setStatusLocked(st Status) {
	old := m.status
	if st.State != old.State {
		st.Since = time.Now()
	}
	if statusEqual(st, old) {
		return
	}
	m.status = st
	cp := st
	m.hub.publish(Event{Type: "status", Status: &cp})
}

// run owns the connection: transport, then engine after engine until the
// rider releases or something unrecoverable happens.
func (m *Manager) run(c *conn) {
	defer m.wg.Done()
	defer m.finish(c)

	m.mu.Lock()
	m.startHR(c)
	m.mu.Unlock()
	for {
		m.mu.Lock()
		if c.releasing || c.ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		sup := bike.NewSupervisor(m.dialFor(c.plan, c.rider, c.tr),
			bike.SupervisorOptions{MinLevel: c.rider.MinLevel, MaxLevel: c.rider.MaxLevel})
		tp := newTap(sup, func(ev bike.Event) { m.onEvent(c, ev) })
		var hr engine.HeartRate
		if c.hrMon != nil {
			hr = c.hrMon
		}
		eng := engine.New(engine.Config{
			Driver:     tp,
			HeartRate:  hr,
			StartLevel: clamp(int(math.Round(c.rider.OffsetLevel)), c.rider.MinLevel, c.rider.MaxLevel),
			Physics: engine.Physics{MassKg: c.rider.WeightKg + c.rider.BikeKg, CdA: c.rider.CdA,
				Crr: c.rider.Crr, Rho: 1.225},
			TickEvery:       m.d.TickEvery,
			PowerModel:      c.model,
			PowerMultiplier: c.rider.PowerMultiplier,
		})
		ticks, unsub := eng.Subscribe(256)
		c.eng, c.tap = eng, tp
		c.state, c.connected = engine.StateConnecting, false
		m.pushLocked(c)
		m.mu.Unlock()

		fwdDone := make(chan struct{})
		go func() {
			defer close(fwdDone)
			for tk := range ticks {
				m.onTick(c, eng, tk)
			}
		}()
		err := eng.Run(c.ctx)
		unsub()
		<-fwdDone
		<-tp.done
		cerr := eng.CloseErr()
		m.saveModel(c)

		m.mu.Lock()
		releasing := c.releasing
		ever := tp.everConnected
		lo := c.rider.MinLevel
		// Only an engine whose session came up says anything about the floor;
		// a reconnect engine that never connected must not erase an earlier
		// failure (the bike may still be at its last level).
		if ever {
			if cerr != nil {
				c.relErr = fmt.Errorf("bike not confirmed back at level %d: %w", lo, cerr)
				c.floorWarn = fmt.Sprintf("bike not confirmed back at level %d (%v); lower resistance at the console", lo, cerr)
			} else {
				c.relErr, c.floorWarn = nil, ""
			}
		}
		if err != nil && !releasing && c.ctx.Err() == nil && c.connErr == "" {
			c.connErr = err.Error()
		}
		again := err == nil && !releasing && c.ctx.Err() == nil
		if again {
			c.lastActive = time.Now()
			c.eng, c.tap = nil, nil
			c.state, c.connected = engine.StateConnecting, false
			m.pushLocked(c)
		}
		m.mu.Unlock()
		if ever && cerr != nil {
			m.log.Warn("bike not confirmed at minimum level", "err", cerr)
		}
		if !again {
			return
		}
	}
}

// finish tears the connection down and publishes idle.
func (m *Manager) finish(c *conn) {
	// The ride is over and its bike closed: stop heart rate, then free the
	// transport they shared.
	c.stopHR()
	if c.tr != nil {
		_ = c.tr.Close()
	}
	m.mu.Lock()
	c.finished = true
	if m.cur == c {
		m.cur = nil
	}
	c.eng, c.tap = nil, nil
	m.setStatusLocked(Status{State: string(engine.StateIdle), LastError: c.lastError(), Hint: c.hint, FloorUnconfirmed: c.floorWarn != "", Since: m.status.Since})
	m.mu.Unlock()
	c.cancel()
	close(c.done)
	m.log.Info("bike released", "rider", c.rider.ID)
}

// onTick forwards an engine tick and folds its state into the status.
func (m *Manager) onTick(c *conn, eng *engine.Engine, tk engine.Tick) {
	m.hub.publish(Event{Type: "tick", Tick: &tk})
	if tk.State == engine.StateFinished {
		return // a replacement engine reports the next state
	}
	st := tk.State
	if st == engine.StateIdle {
		st = engine.StateConnecting
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.eng != eng {
		return
	}
	if st != c.state && (st == engine.StateConnecting || st == engine.StateReady) {
		c.lastActive = time.Now()
	}
	if st == engine.StateRiding {
		c.floorWarn = ""
	}
	c.state, c.connected = st, tk.Connected
	if p := c.tap.Caps().Protocol; p != bike.ProtoUnknown {
		c.proto = p.String()
	}
	m.pushLocked(c)
}

// onEvent folds a driver event into connection errors and hints.
func (m *Manager) onEvent(c *conn, ev bike.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch ev.Kind {
	case bike.EventConnected:
		c.connErr, c.hint = "", ""
		if c.tap != nil {
			c.tap.everConnected = true
			if p := c.tap.Caps().Protocol; p != bike.ProtoUnknown {
				c.proto = p.String()
			}
		}
	case bike.EventDisconnected:
		if ev.Err == nil {
			return
		}
		c.connErr, c.hint = ev.Err.Error(), hintFor(ev.Err)
	default:
		return
	}
	m.pushLocked(c)
}

// idleWatch releases the connection when it sits connecting or ready for d
// without a command.
func (m *Manager) idleWatch(c *conn, d time.Duration) {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		wait := time.Until(c.lastActive.Add(d))
		if wait <= 0 {
			if (c.state == engine.StateConnecting || c.state == engine.StateReady) && !c.releasing {
				m.mu.Unlock()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = m.release(ctx, fmt.Sprintf("released after %d min idle", int(d/time.Minute)), false)
				cancel()
				return
			}
			c.lastActive = time.Now() // riding or paused: the clock restarts when it stops
			wait = d
		}
		m.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-c.ctx.Done():
			t.Stop()
			return
		case <-c.done:
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// tap is a Driver that copies the supervisor's events through a callback so
// the manager sees connection errors; the engine reads the copied stream.
type tap struct {
	bike.Driver
	out  chan bike.Event
	done chan struct{} // closed once the event stream is drained

	everConnected bool // a session came up at least once; guarded by Manager.mu
}

func newTap(d bike.Driver, on func(bike.Event)) *tap {
	t := &tap{Driver: d, out: make(chan bike.Event, 32), done: make(chan struct{})}
	in := d.Events()
	go func() {
		defer close(t.done)
		defer close(t.out)
		for ev := range in {
			on(ev)
			push(t.out, ev)
		}
	}()
	return t
}

func (t *tap) Events() <-chan bike.Event { return t.out }

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }
