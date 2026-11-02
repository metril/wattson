package esphomeapi

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/flynn/noise"
)

func TestWriteMsgDeadlineOnStalledPeer(t *testing.T) {
	t.Parallel()
	a, b := net.Pipe() // nothing reads b: every write blocks
	t.Cleanup(func() { a.Close(); b.Close() })
	sc := &secureConn{c: a, send: noise.UnsafeNewCipherState(noiseSuite, [32]byte{1}, 0), wtimeout: 30 * time.Millisecond}
	start := time.Now()
	err := sc.WriteMsg(MsgPingRequest, nil)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %v", el)
	}
}

func TestWriteTimeoutDerivedFromPingInterval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want time.Duration }{
		{10 * time.Second, 20 * time.Second},
		{100 * time.Millisecond, time.Second},
	} {
		if got := writeTimeout(tc.in); got != tc.want {
			t.Errorf("writeTimeout(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
