package esphomeapi

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/metril/wattson/internal/esphomeapi/esphometest"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestHandshakeAndRoundTrip(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, key(1))
	srv.Handle(7, func([]byte) []esphometest.Reply { return []esphometest.Reply{{Type: 8}} })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sc, _, err := clientHandshake(ctx, dial(t, srv.Addr()), key(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.WriteMsg(7, nil); err != nil {
		t.Fatal(err)
	}
	typ, p, err := sc.ReadMsg()
	if err != nil || typ != 8 || len(p) != 0 {
		t.Fatalf("got typ=%d payload=%x err=%v", typ, p, err)
	}
	// Server push with payload, several in a row (nonce progression).
	for i := 0; i < 3; i++ {
		srv.Push(9, []byte{byte(i), 0xAA})
		typ, p, err = sc.ReadMsg()
		if err != nil || typ != 9 || !bytes.Equal(p, []byte{byte(i), 0xAA}) {
			t.Fatalf("push %d: typ=%d payload=%x err=%v", i, typ, p, err)
		}
	}
}

func TestHandshakeWrongKey(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, key(1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := clientHandshake(ctx, dial(t, srv.Addr()), key(2))
	if !errors.Is(err, ErrBadKey) {
		t.Fatalf("want ErrBadKey, got %v", err)
	}
}

func TestServerHelloParsed(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, key(1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, h, err := clientHandshake(ctx, dial(t, srv.Addr()), key(1))
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "wattson-proxy" || h.MAC != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("hello = %+v", h)
	}
}

func TestHandshakeCtxTimeout(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { // accept and stay silent
		c, err := ln.Accept()
		if err == nil {
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = clientHandshake(ctx, dial(t, ln.Addr().String()), key(1))
	if err == nil {
		t.Fatal("expected error")
	}
	if el := time.Since(start); el > 400*time.Millisecond {
		t.Fatalf("took %v", el)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}

func TestCloseClientEndsRead(t *testing.T) {
	t.Parallel()
	srv := esphometest.Start(t, key(1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sc, _, err := clientHandshake(ctx, dial(t, srv.Addr()), key(1))
	if err != nil {
		t.Fatal(err)
	}
	srv.Push(1, nil)
	if _, _, err := sc.ReadMsg(); err != nil {
		t.Fatal(err)
	}
	srv.CloseClient()
	if _, _, err := sc.ReadMsg(); err == nil {
		t.Fatal("expected read error after CloseClient")
	}
}
