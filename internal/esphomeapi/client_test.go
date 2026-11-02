package esphomeapi

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

func keyB64(b byte) string { return base64.StdEncoding.EncodeToString(key(b)) }

// newServer starts a fake device that answers hello and device info.
func newServer(t *testing.T, flags uint32) *esphometest.Server {
	t.Helper()
	srv := esphometest.Start(t, key(1))
	srv.Handle(MsgHelloRequest, func([]byte) []esphometest.Reply {
		return []esphometest.Reply{{Type: MsgHelloResponse, Payload: HelloResponse{APIVersionMajor: 1, APIVersionMinor: 13, Name: esphometest.Name}.Marshal()}}
	})
	srv.Handle(MsgDeviceInfoRequest, func([]byte) []esphometest.Reply {
		return []esphometest.Reply{{Type: MsgDeviceInfoResponse, Payload: DeviceInfoResponse{
			Name: esphometest.Name, MACAddress: esphometest.MAC, ESPHomeVersion: "2026.10.1", BTProxyFeatureFlags: flags,
		}.Marshal()}}
	})
	return srv
}

func dialOpts(t *testing.T, srv *esphometest.Server, o DialOptions) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, srv.Addr(), keyB64(1), o)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func waitDone(t *testing.T, c *Client, d time.Duration) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(d):
		t.Fatalf("client not done within %v", d)
	}
}

func TestDialReadsDeviceInfo(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 2|4|32)
	c := dialOpts(t, srv, DialOptions{})
	want := DeviceInfo{Name: esphometest.Name, MAC: esphometest.MAC, ESPHomeVersion: "2026.10.1", BTProxyFlags: 38}
	if got := c.Info(); got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestDialSendsHelloClientInfo(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	got := make(chan HelloRequest, 1)
	srv.Handle(MsgHelloRequest, func(p []byte) []esphometest.Reply {
		var h HelloRequest
		_ = h.Unmarshal(p)
		got <- h
		return []esphometest.Reply{{Type: MsgHelloResponse, Payload: HelloResponse{APIVersionMajor: 1, APIVersionMinor: 13}.Marshal()}}
	})
	dialOpts(t, srv, DialOptions{})
	if h := <-got; h.ClientInfo != "wattson" || h.APIVersionMajor != 1 || h.APIVersionMinor != 10 {
		t.Fatalf("hello = %+v", h)
	}
}

func TestDialErrors(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, srv.Addr(), keyB64(9)); !errors.Is(err, ErrBadKey) {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := Dial(ctx, srv.Addr(), "not base64!!"); !errors.Is(err, ErrBadKey) {
		t.Fatalf("bad base64: %v", err)
	}
}

func TestDialContextTimeoutWhenNoHelloResponse(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, key(1)) // handshakes but never answers hello
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Dial(ctx, srv.Addr(), keyB64(1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestClientAnswersServerPing(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	pong := make(chan struct{}, 1)
	srv.Handle(MsgPingResponse, func([]byte) []esphometest.Reply { pong <- struct{}{}; return nil })
	c := dialOpts(t, srv, DialOptions{})
	srv.Push(MsgPingRequest, nil)
	select {
	case <-pong:
	case <-c.Done():
		t.Fatal("client closed")
	case <-time.After(2 * time.Second):
		t.Fatal("no PingResponse")
	}
}

func TestClientPingTimeoutCloses(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	srv.Handle(MsgPingRequest, func([]byte) []esphometest.Reply { return nil }) // drop pings
	c := dialOpts(t, srv, DialOptions{PingInterval: 50 * time.Millisecond})
	waitDone(t, c, 300*time.Millisecond)
	if !errors.Is(c.Err(), ErrTimeout) {
		t.Fatalf("Err = %v", c.Err())
	}
}

func TestClientKeepaliveStaysUp(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	pings := make(chan struct{}, 16)
	srv.Handle(MsgPingRequest, func([]byte) []esphometest.Reply {
		pings <- struct{}{}
		return []esphometest.Reply{{Type: MsgPingResponse}}
	})
	c := dialOpts(t, srv, DialOptions{PingInterval: 10 * time.Millisecond})
	for i := 0; i < 5; i++ {
		select {
		case <-pings:
		case <-c.Done():
			t.Fatalf("closed early: %v", c.Err())
		case <-time.After(2 * time.Second):
			t.Fatal("no ping")
		}
	}
	select {
	case <-c.Done():
		t.Fatalf("closed: %v", c.Err())
	default:
	}
}

func TestServerDisconnectRequest(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	resp := make(chan struct{}, 1)
	srv.Handle(MsgDisconnectResponse, func([]byte) []esphometest.Reply { resp <- struct{}{}; return nil })
	c := dialOpts(t, srv, DialOptions{})
	srv.Push(MsgDisconnectRequest, nil)
	waitDone(t, c, 2*time.Second)
	if !errors.Is(c.Err(), ErrDisconnected) {
		t.Fatalf("Err = %v", c.Err())
	}
	select {
	case <-resp:
	case <-time.After(2 * time.Second):
		t.Fatal("no DisconnectResponse")
	}
}

func TestCloseSendsDisconnectAndIsIdempotent(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	got := make(chan struct{}, 1)
	srv.Handle(MsgDisconnectRequest, func([]byte) []esphometest.Reply { got <- struct{}{}; return nil })
	c := dialOpts(t, srv, DialOptions{})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, c, time.Second)
	if c.Err() != nil {
		t.Fatalf("Err after Close = %v", c.Err())
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("server never saw DisconnectRequest")
	}
	if err := c.Send(MsgPingRequest, Empty{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close: %v", err)
	}
}

func TestConnectionDropClosesDone(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	c := dialOpts(t, srv, DialOptions{})
	srv.CloseClient()
	waitDone(t, c, 2*time.Second)
	if c.Err() == nil {
		t.Fatal("expected error")
	}
}

func TestOnDispatchAndCancel(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	c := dialOpts(t, srv, DialOptions{})
	a, b := make(chan []byte, 4), make(chan []byte, 4)
	cancelA := c.On(MsgGATTReadResponse, func(p []byte) { a <- p })
	c.On(MsgGATTReadResponse, func(p []byte) { b <- p })
	other := make(chan struct{}, 1)
	c.On(MsgGATTWriteResponse, func([]byte) { other <- struct{}{} })

	srv.Push(MsgGATTReadResponse, []byte{1})
	if p := <-a; len(p) != 1 {
		t.Fatalf("a: %x", p)
	}
	<-b
	cancelA()
	cancelA() // idempotent
	srv.Push(MsgGATTReadResponse, []byte{2})
	if p := <-b; p[0] != 2 {
		t.Fatalf("b: %x", p)
	}
	select {
	case p := <-a:
		t.Fatalf("cancelled handler called: %x", p)
	case <-other:
		t.Fatal("wrong type dispatched")
	default:
	}
}

func TestSendMarshalsMessage(t *testing.T) {
	t.Parallel()
	srv := newServer(t, 0)
	got := make(chan []byte, 1)
	srv.Handle(MsgBLEDeviceRequest, func(p []byte) []esphometest.Reply { got <- p; return nil })
	c := dialOpts(t, srv, DialOptions{})
	if err := c.Send(MsgBLEDeviceRequest, BLEDeviceRequest{Address: 7, RequestType: BLEReqConnectWithCache}); err != nil {
		t.Fatal(err)
	}
	var m BLEDeviceRequest
	if err := m.Unmarshal(<-got); err != nil || m.Address != 7 || m.RequestType != 4 {
		t.Fatalf("%+v %v", m, err)
	}
}
