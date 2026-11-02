package bike

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/metril/wattson/internal/blue"
)

const (
	dialConnectTimeout = 30 * time.Second
	dialOpenTimeout    = 15 * time.Second
)

// BLEDial returns a Dial that connects to addr over tr and opens a *Bike.
// Connect is bounded at 30s and Open at 15s, both derived from the dial ctx.
// If Open fails the connection is closed.
func BLEDial(tr blue.Transport, addr blue.Address, o Options) Dial {
	return func(ctx context.Context) (Session, error) {
		cctx, cancel := context.WithTimeout(ctx, dialConnectTimeout)
		conn, err := tr.Connect(cctx, addr)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				// Our own bound expired before a link came up. ErrNotFound only
				// reflects that: callers must not treat it as proof the device
				// is absent (it may be busy, out of range or slow to connect).
				err = fmt.Errorf("%w: no link within %s: %w", blue.ErrNotFound, dialConnectTimeout, err)
			}
			return nil, err
		}
		octx, cancel := context.WithTimeout(ctx, dialOpenTimeout)
		defer cancel()
		b, err := Open(octx, conn, o)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return b, nil
	}
}
