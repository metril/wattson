package bike

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/blue"
	"github.com/metril/wattson/internal/blue/bluetest"
)

// connSpy records the connections a transport hands out.
type connSpy struct {
	blue.Transport
	mu    sync.Mutex
	conns []blue.Conn
}

func (s *connSpy) Connect(ctx context.Context, a blue.Address) (blue.Conn, error) {
	c, err := s.Transport.Connect(ctx, a)
	if err == nil {
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
	}
	return c, err
}

func TestBLEDialOpensFitPlus(t *testing.T) {
	t.Parallel()
	f := bluetest.NewFake(fitPlusDevice())
	defer f.Close()
	s, err := BLEDial(f, testAddr, Options{Keepalive: time.Hour})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if s.Protocol() != ProtoFitPlus || s.MaxLevel() != 16 {
		t.Fatalf("proto %v max %d", s.Protocol(), s.MaxLevel())
	}
	if w := f.Writes(testAddr); len(w) == 0 || !bytes.Equal(w[0], []byte{0xaa, 0x01, 0x00, 0x01, 0x55}) {
		t.Fatalf("writes % x", w)
	}
}

func TestBLEDialUnsupportedClosesConn(t *testing.T) {
	t.Parallel()
	f := bluetest.NewFake(&bluetest.Device{
		Adv:      blue.Advertisement{Addr: testAddr},
		Services: []blue.Service{{UUID: blue.UUID16(0x180a)}},
	})
	defer f.Close()
	spy := &connSpy{Transport: f}
	_, err := BLEDial(spy, testAddr, Options{})(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if len(spy.conns) != 1 {
		t.Fatalf("conns %d", len(spy.conns))
	}
	select {
	case <-spy.conns[0].Done():
	default:
		t.Fatal("conn leaked: Done not closed")
	}
}

func TestBLEDialUnknownAddr(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake()
		defer f.Close()
		_, err := BLEDial(f, testAddr, Options{})(context.Background())
		if !errors.Is(err, blue.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBLEDialCallerCancelNotNotFound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := bluetest.NewFake()
		defer f.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := BLEDial(f, testAddr, Options{})(ctx)
		if errors.Is(err, blue.ErrNotFound) || !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	})
}
