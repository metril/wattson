// Package mqtttest runs an in-process MQTT broker (mochi-mqtt) on a random
// loopback port for tests.
package mqtttest

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// Message is one message seen by Watch.
type Message struct {
	Topic   string
	Payload []byte
	Retain  bool // the retain flag as the broker holds the message
}

// Broker is a running test broker.
type Broker struct {
	srv  *mqtt.Server
	addr string
	subs atomic.Int64
}

type authHook struct {
	mqtt.HookBase
	users map[string]string // nil allows everyone
}

func (h *authHook) ID() string { return "mqtttest-auth" }

func (h *authHook) Provides(b byte) bool {
	return b == mqtt.OnConnectAuthenticate || b == mqtt.OnACLCheck
}

func (h *authHook) OnConnectAuthenticate(_ *mqtt.Client, pk packets.Packet) bool {
	if h.users == nil {
		return true
	}
	pw, ok := h.users[string(pk.Connect.Username)]
	return ok && pw == string(pk.Connect.Password)
}

func (h *authHook) OnACLCheck(*mqtt.Client, string, bool) bool { return true }

// Start listens on 127.0.0.1:0. users maps username to password; nil allows
// every client.
func Start(t testing.TB, users map[string]string) *Broker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return StartOn(t, ln, users)
}

// StartOn serves on an existing listener.
func StartOn(t testing.TB, ln net.Listener, users map[string]string) *Broker {
	t.Helper()
	srv := mqtt.New(&mqtt.Options{InlineClient: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.AddHook(&authHook{users: users}, nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewNet("test", ln)); err != nil {
		t.Fatal(err)
	}
	if err := srv.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// mochi deadlocks when Close races a client that is still being
		// removed (Clients.GetByListener takes a read lock twice), so let
		// the clients drain first.
		for i := 0; i < 200 && srv.Clients.Len() > 0; i++ {
			time.Sleep(5 * time.Millisecond)
		}
		_ = srv.Close()
	})
	return &Broker{srv: srv, addr: ln.Addr().String()}
}

// URL is the broker's mqtt:// URL.
func (b *Broker) URL() string { return "mqtt://" + b.addr }

// Retained returns the retained payload of topic, or nil when none.
func (b *Broker) Retained(topic string) []byte {
	pk, ok := b.srv.Topics.Retained.Get(topic)
	if !ok || len(pk.Payload) == 0 {
		return nil
	}
	return append([]byte(nil), pk.Payload...)
}

// Watch delivers the retained messages that already match filter, then every
// message published to it. The channel is buffered and drops messages when
// full.
func (b *Broker) Watch(filter string) <-chan Message {
	ch := make(chan Message, 256)
	err := b.srv.Subscribe(filter, int(b.subs.Add(1)), func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
		m := Message{Topic: pk.TopicName, Payload: append([]byte(nil), pk.Payload...), Retain: pk.FixedHeader.Retain}
		select {
		case ch <- m:
		default:
		}
	})
	if err != nil {
		panic("mqtttest: watch " + filter + ": " + err.Error())
	}
	return ch
}

// Publish injects a message as the broker itself.
func (b *Broker) Publish(topic string, payload []byte, retain bool) {
	if err := b.srv.Publish(topic, payload, retain, 1); err != nil {
		panic("mqtttest: publish " + topic + ": " + err.Error())
	}
}

// KickAll drops every client connection without a DISCONNECT, which fires
// their last wills.
func (b *Broker) KickAll() {
	for _, cl := range b.srv.Clients.GetAll() {
		if cl.Net.Inline || strings.HasPrefix(cl.ID, "inline") {
			continue
		}
		cl.Stop(errors.New("mqtttest: kicked"))
	}
}
