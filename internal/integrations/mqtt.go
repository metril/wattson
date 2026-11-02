// Package integrations holds the outbound integrations that are not riders'
// personal accounts: the Home Assistant MQTT bridge.
package integrations

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"

	"github.com/metril/wattson/internal/engine"
	"github.com/metril/wattson/internal/ridectl"
	"github.com/metril/wattson/internal/settings"
)

// RideControl is the part of ridectl.Manager the bridge drives.
type RideControl interface {
	Do(ctx context.Context, c engine.Command) error
	Release(ctx context.Context) error
	Subscribe(buf int) (<-chan ridectl.Event, func())
}

// MQTTDeps are the bridge's collaborators.
type MQTTDeps struct {
	Settings   func(context.Context) (settings.Settings, error)
	Ride       RideControl
	Version    string
	ClientID   string        // MQTT client id; stable so a stale session is replaced
	RetryDelay time.Duration // between connection attempts; zero means 5 s
	Logger     *slog.Logger
}

// MQTTStatus is the bridge's connection state.
type MQTTStatus struct {
	Enabled   bool   `json:"enabled" doc:"Whether an MQTT broker URL is configured."`
	Connected bool   `json:"connected" doc:"Whether the bridge is connected to the broker right now."`
	Error     string `json:"error,omitempty" doc:"Most recent connection problem, such as a refused login or an unreachable broker; absent when connected or disabled."`
}

const (
	keepAliveS     = 30
	connectTimeout = 10 * time.Second
	publishTimeout = 5 * time.Second
	closeTimeout   = 2 * time.Second
	commandTimeout = 20 * time.Second // a release waits for the bike to confirm its floor
	stateEvery     = time.Second      // at most one state publish per second
	defaultRetry   = 5 * time.Second
)

// MQTT is the Home Assistant bridge: it publishes availability, discovery and
// ride state to an MQTT broker and maps button presses onto ride commands.
// The broker is trusted: commands carry no user identity.
type MQTT struct {
	d       MQTTDeps
	log     *slog.Logger
	reload  chan struct{}
	events  chan sessionEvent
	every   time.Duration // state publish interval; tests shorten it
	mu      sync.Mutex
	status  MQTTStatus
	lastErr string

	// Owned by Run.
	sess      *session
	latest    MQTTState
	haveLast  bool
	pubState  MQTTState
	havePub   bool
	lastPubAt time.Time
	timer     *time.Timer
	timerC    <-chan time.Time
}

type eventKind int

const (
	evUp eventKind = iota
	evDown
	evErr
)

type sessionEvent struct {
	s    *session
	kind eventKind
	err  error
}

// session is one broker connection for one settings snapshot.
type session struct {
	cfg       settings.MQTT
	cm        *autopaho.ConnectionManager
	ctx       context.Context
	cancel    context.CancelFunc
	cmds      chan string
	workerEnd chan struct{}
	up        bool
	pubPrefix string // discovery prefix whose configs are currently published; "" none
}

// NewMQTT builds the bridge; Run starts it.
func NewMQTT(d MQTTDeps) *MQTT {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = defaultRetry
	}
	return &MQTT{d: d, log: d.Logger.With("component", "mqtt"), reload: make(chan struct{}, 1),
		events: make(chan sessionEvent, 32), every: stateEvery}
}

// Reload re-reads the settings and reconnects if the mqtt section changed.
// It never blocks.
func (m *MQTT) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// Status reports the connection state.
func (m *MQTT) Status() MQTTStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *MQTT) setStatus(connected bool, errText string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Connected, m.status.Error = connected, errText
}

// Run drives the bridge until ctx ends: it publishes "offline" and returns
// ctx.Err(). With no broker URL configured it idles until Reload.
func (m *MQTT) Run(ctx context.Context) error {
	events, unsub := m.d.Ride.Subscribe(64)
	defer unsub()
	defer func() {
		if m.timer != nil {
			m.timer.Stop()
		}
	}()
	m.apply(ctx)
	for {
		select {
		case <-ctx.Done():
			if m.sess != nil {
				m.closeSession(m.sess, false)
				m.sess = nil
			}
			return ctx.Err()
		case <-m.reload:
			m.apply(ctx)
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			m.onRideEvent(ctx, ev)
		case ev := <-m.events:
			m.onSessionEvent(ctx, ev)
		case <-m.timerC:
			m.timerC = nil
			m.flushState(ctx)
		}
	}
}

func (m *MQTT) onRideEvent(ctx context.Context, ev ridectl.Event) {
	switch {
	case ev.Type == "tick" && ev.Tick != nil:
		m.latest, m.haveLast = StateFromTick(*ev.Tick), true
	case ev.Type == "status" && ev.Status != nil && ev.Status.State == string(engine.StateIdle):
		// Releasing the bike publishes no idle tick.
		m.latest, m.haveLast = StateFromTick(engine.Tick{State: engine.StateIdle}), true
	default:
		return
	}
	m.flushState(ctx)
}

// flushState publishes the latest state when it changed, at most once per
// m.every; a change inside the interval is published when it ends.
func (m *MQTT) flushState(ctx context.Context) {
	s := m.sess
	if s == nil || !s.up || !m.haveLast || (m.havePub && m.latest == m.pubState) {
		return
	}
	if wait := m.every - time.Since(m.lastPubAt); m.havePub && wait > 0 {
		if m.timerC == nil {
			if m.timer == nil {
				m.timer = time.NewTimer(wait)
			} else {
				m.timer.Reset(wait)
			}
			m.timerC = m.timer.C
		}
		return
	}
	b, _ := json.Marshal(m.latest)
	if err := m.publish(ctx, s, topicState, b, 0, true); err != nil {
		m.log.Debug("publish state", "err", err)
		return
	}
	m.pubState, m.havePub, m.lastPubAt = m.latest, true, time.Now()
}

func (m *MQTT) publish(ctx context.Context, s *session, topic string, payload []byte, qos byte, retain bool) error {
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	_, err := s.cm.Publish(ctx, &paho.Publish{Topic: topic, QoS: qos, Retain: retain, Payload: payload})
	return err
}

// apply reads the settings and brings the connection in line with them.
func (m *MQTT) apply(ctx context.Context) {
	st, err := m.d.Settings(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Warn("read settings", "err", err)
		}
		return
	}
	want := st.MQTT
	if s := m.sess; s != nil {
		if s.cfg == want {
			return
		}
		if sameConnection(s.cfg, want) {
			s.cfg.DiscoveryPrefix = want.DiscoveryPrefix
			if s.up {
				m.syncDiscovery(ctx, s)
			}
			return
		}
		// The same broker with new credentials keeps its retained discovery:
		// only another URL, or switching the bridge off, clears it.
		clear := want.URL == "" || want.URL != s.cfg.URL
		if !clear && s.up && s.pubPrefix != "" && s.pubPrefix != want.DiscoveryPrefix {
			// Credentials and prefix changed together: the new session starts
			// with no record of the old prefix, so empty its configs now.
			m.clearDiscovery(ctx, s, s.pubPrefix)
			s.pubPrefix = ""
		}
		m.closeSession(s, clear)
		m.sess = nil
	}
	m.havePub = false
	m.mu.Lock()
	m.status = MQTTStatus{Enabled: want.URL != ""}
	m.lastErr = ""
	m.mu.Unlock()
	if want.URL == "" {
		return
	}
	s, err := m.open(ctx, want)
	if err != nil {
		m.log.Warn("mqtt not started", "err", err)
		m.setStatus(false, err.Error())
		return
	}
	m.sess = s
}

func sameConnection(a, b settings.MQTT) bool {
	return a.URL == b.URL && a.Username == b.Username && a.Password == b.Password &&
		a.TLSInsecureSkipVerify == b.TLSInsecureSkipVerify
}

// open starts connecting to the broker; the connection comes up (or fails)
// asynchronously and is reported through m.events.
func (m *MQTT) open(ctx context.Context, cfg settings.MQTT) (*session, error) {
	u, err := brokerURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	// The session outlives ctx so shutdown can publish "offline" first.
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s := &session{cfg: cfg, ctx: sctx, cancel: cancel, cmds: make(chan string, 16), workerEnd: make(chan struct{})}
	emit := func(k eventKind, err error) {
		select {
		case m.events <- sessionEvent{s: s, kind: k, err: err}:
		case <-sctx.Done():
		}
	}
	var everUp atomic.Bool
	cc := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		TlsCfg:                        tlsConfig(u, cfg.TLSInsecureSkipVerify),
		KeepAlive:                     keepAliveS,
		CleanStartOnInitialConnection: true,
		SessionExpiryInterval:         0,
		ReconnectBackoff: func(attempt int) time.Duration {
			if attempt == 0 && !everUp.Load() {
				return 0 // the first connection does not wait
			}
			return m.d.RetryDelay // a lost connection is retried after the delay, not at once
		},
		ConnectTimeout:  connectTimeout,
		ConnectUsername: cfg.Username,
		ConnectPassword: []byte(cfg.Password),
		WillMessage:     &paho.WillMessage{Topic: topicStatus, Payload: []byte(payloadOffline), QoS: 1, Retain: true},
		OnConnectionUp: func(*autopaho.ConnectionManager, *paho.Connack) {
			everUp.Store(true)
			emit(evUp, nil)
		},
		OnConnectError: func(err error) { emit(evErr, err) },
		ClientConfig: paho.ClientConfig{
			ClientID: m.d.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) { m.onMessage(s, pr.Packet); return true, nil },
			},
			OnClientError: func(err error) { emit(evDown, err) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				emit(evDown, fmt.Errorf("the broker closed the connection (reason 0x%02x)", d.ReasonCode))
			},
		},
	}
	cm, err := autopaho.NewConnection(sctx, cc)
	if err != nil {
		cancel()
		return nil, err
	}
	s.cm = cm
	go m.worker(s)
	return s, nil
}

func (m *MQTT) onSessionEvent(ctx context.Context, ev sessionEvent) {
	s := ev.s
	if s != m.sess {
		return // a session that has been replaced
	}
	switch ev.kind {
	case evUp:
		s.up = true
		m.setStatus(true, "")
		m.lastErr = ""
		m.log.Info("mqtt connected", "url", s.cfg.URL)
		m.havePub = false
		sub := &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: commandFilter, QoS: 1, RetainHandling: 2}}}
		sctx, cancel := context.WithTimeout(ctx, publishTimeout)
		_, err := s.cm.Subscribe(sctx, sub)
		cancel()
		if err != nil {
			m.log.Warn("subscribe to commands", "err", err)
		}
		if err := m.publish(ctx, s, topicStatus, []byte(payloadOnline), 1, true); err != nil {
			m.log.Warn("publish availability", "err", err)
		}
		m.syncDiscovery(ctx, s)
		m.flushState(ctx)
	case evDown, evErr:
		s.up = false
		text := describeErr(ev.err)
		if ev.kind == evDown {
			text = "connection lost: " + text
		}
		m.setStatus(false, text)
		if text != m.lastErr { // a down broker is retried forever: log each distinct cause once
			m.lastErr = text
			m.log.Warn("mqtt not connected", "err", text)
		}
	}
}

// syncDiscovery publishes the discovery configs under the current prefix,
// first emptying those of a previous prefix.
func (m *MQTT) syncDiscovery(ctx context.Context, s *session) {
	prefix := s.cfg.DiscoveryPrefix
	if s.pubPrefix == prefix {
		if s.pubPrefix != "" {
			return
		}
	} else if s.pubPrefix != "" {
		m.clearDiscovery(ctx, s, s.pubPrefix)
		s.pubPrefix = ""
	}
	failed := false
	for _, msg := range Discovery(prefix, m.d.Version) {
		if err := m.publish(ctx, s, msg.Topic, msg.Payload, 1, true); err != nil {
			m.log.Warn("publish discovery", "topic", msg.Topic, "err", err)
			failed = true
			break
		}
	}
	if !failed {
		s.pubPrefix = prefix
	}
}

func (m *MQTT) clearDiscovery(ctx context.Context, s *session, prefix string) {
	for _, msg := range Discovery(prefix, "") {
		if err := m.publish(ctx, s, msg.Topic, nil, 1, true); err != nil {
			m.log.Warn("clear discovery", "topic", msg.Topic, "err", err)
			return
		}
	}
}

// closeSession publishes "offline" and disconnects. With clear the retained
// discovery configs and state are emptied too (the bridge was disabled or
// pointed elsewhere); at shutdown they stay for the next start.
func (m *MQTT) closeSession(s *session, clear bool) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if s.up {
		if clear {
			if s.pubPrefix != "" {
				m.clearDiscovery(ctx, s, s.pubPrefix)
			}
			_ = m.publish(ctx, s, topicState, nil, 1, true)
		}
		if err := m.publish(ctx, s, topicStatus, []byte(payloadOffline), 1, true); err != nil {
			m.log.Warn("publish offline", "err", err)
		}
	}
	_ = s.cm.Disconnect(ctx)
	s.cancel()
	<-s.workerEnd
	s.up = false
	m.setStatus(false, "")
}

// onMessage queues a command press; it runs on the client's read goroutine so
// it only enqueues.
func (m *MQTT) onMessage(s *session, p *paho.Publish) {
	if p.Retain { // a retained press is stale: never execute it
		return
	}
	key, ok := strings.CutPrefix(p.Topic, commandPrefix)
	if !ok || key == "" || strings.Contains(key, "/") {
		return
	}
	select {
	case s.cmds <- key:
	default:
		m.log.Warn("command queue full; dropping", "command", key)
	}
}

// worker runs queued commands one at a time, in order.
func (m *MQTT) worker(s *session) {
	defer close(s.workerEnd)
	for {
		select {
		case <-s.ctx.Done():
			return
		case key := <-s.cmds:
			m.runCommand(s.ctx, key)
		}
	}
}

func (m *MQTT) runCommand(ctx context.Context, key string) {
	var do func(context.Context) error
	cmd := func(c engine.Command) func(context.Context) error {
		return func(ctx context.Context) error { return m.d.Ride.Do(ctx, c) }
	}
	switch key {
	case "start": // a free ride on the connected bike
		do = cmd(engine.Command{Kind: engine.CmdStart, Mode: engine.FreeMode{}})
	case "pause":
		do = cmd(engine.Command{Kind: engine.CmdPause})
	case "resume":
		do = cmd(engine.Command{Kind: engine.CmdResume})
	case "stop":
		do = cmd(engine.Command{Kind: engine.CmdStop})
	case "level_up":
		do = cmd(engine.Command{Kind: engine.CmdLevelDelta, Value: 1})
	case "level_down":
		do = cmd(engine.Command{Kind: engine.CmdLevelDelta, Value: -1})
	case "release_bike":
		do = m.d.Ride.Release
	default:
		m.log.Warn("unknown mqtt command", "command", key)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	if err := do(ctx); err != nil {
		m.log.Warn("mqtt command failed", "command", key, "err", err)
	}
}

func tlsConfig(u *url.URL, insecure bool) *tls.Config {
	return &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec // opt-in for self-signed LAN brokers
}

// brokerURL parses an mqtt:// or mqtts:// URL and adds the default port.
func brokerURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, errors.New("the MQTT URL is not valid")
	}
	port := u.Port()
	switch u.Scheme {
	case "mqtt":
		if port == "" {
			port = "1883"
		}
	case "mqtts":
		if port == "" {
			port = "8883"
		}
	default:
		return nil, errors.New("the MQTT URL must start with mqtt:// or mqtts://")
	}
	u.Host = net.JoinHostPort(u.Hostname(), port)
	return u, nil
}

// connackReasons names the CONNACK failure codes of MQTT 5.
var connackReasons = map[byte]string{
	0x80: "unspecified error", 0x81: "malformed packet", 0x82: "protocol error", 0x83: "implementation specific error",
	0x84: "unsupported protocol version", 0x85: "client identifier not valid", 0x86: "bad user name or password",
	0x87: "not authorized", 0x88: "server unavailable", 0x89: "server busy", 0x8a: "banned",
	0x8c: "bad authentication method", 0x90: "topic name invalid", 0x95: "packet too large", 0x97: "quota exceeded",
	0x99: "payload format invalid", 0x9a: "retain not supported", 0x9b: "QoS not supported", 0x9c: "use another server",
	0x9d: "server moved", 0x9f: "connection rate exceeded",
}

func connackText(code byte) string {
	if r, ok := connackReasons[code]; ok {
		return fmt.Sprintf("the broker refused the connection: %s (CONNACK 0x%02x)", r, code)
	}
	return fmt.Sprintf("the broker refused the connection (CONNACK 0x%02x)", code)
}

func describeErr(err error) string {
	if err == nil {
		return "unknown error"
	}
	var ce *autopaho.ConnackError
	if errors.As(err, &ce) {
		return connackText(ce.ReasonCode)
	}
	return err.Error()
}

// ProbeMQTT makes one connection attempt with a random client id and
// disconnects. insecureTLS skips certificate checks for mqtts. The error says why the broker could not be reached or why it
// refused the login. It never reports the password.
func ProbeMQTT(ctx context.Context, rawURL, username, password string, insecureTLS bool) error {
	u, err := brokerURL(rawURL)
	if err != nil {
		return err
	}
	var conn net.Conn
	if u.Scheme == "mqtts" {
		d := tls.Dialer{Config: tlsConfig(u, insecureTLS)}
		conn, err = d.DialContext(ctx, "tcp", u.Host)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(ctx, "tcp", u.Host)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	var id [6]byte
	_, _ = rand.Read(id[:])
	cli := paho.NewClient(paho.ClientConfig{ClientID: "wattson-test-" + hex.EncodeToString(id[:]), Conn: packets.NewThreadSafeConn(conn)})
	cp := &paho.Connect{KeepAlive: keepAliveS, CleanStart: true, ClientID: "wattson-test-" + hex.EncodeToString(id[:])}
	if username != "" {
		cp.UsernameFlag, cp.Username = true, username
	}
	if password != "" {
		cp.PasswordFlag, cp.Password = true, []byte(password)
	}
	ca, err := cli.Connect(ctx, cp)
	if err != nil {
		if ca != nil && ca.ReasonCode >= 0x80 {
			return errors.New(connackText(ca.ReasonCode))
		}
		return err
	}
	_ = cli.Disconnect(&paho.Disconnect{})
	return nil
}
