package ridectl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/metril/wattson/internal/blue"
)

// proxyProbe bounds the ProxyStatus query made after a failed Connect.
const proxyProbe = 3 * time.Second

var errNotProxy = errors.New("ridectl: transport is not an ESPHome proxy")

// sharedTransport is the one blue.Transport a connection's bike and heart
// rate monitor share (an ESPHome proxy serves a single advertisement
// subscriber, so both must use the same link). It opens lazily on the first
// Scan or Connect and reopens once when the proxy link has closed. It never
// reopens because another client took the proxy's subscription (that would
// steal it back from Home Assistant): ErrProxyHeld is only reported.
//
// It implements blue.Proxy so the heart rate monitor's proxy checks keep
// working; for a non-proxy inner transport ProxyStatus fails and Done never
// closes.
type sharedTransport struct {
	open func(ctx context.Context) (blue.Transport, error)
	sem  chan struct{} // 1-slot lock that waiters can abandon with their ctx

	// Guarded by sem.
	inner  blue.Transport
	closed bool
}

var (
	_ blue.Transport = (*sharedTransport)(nil)
	_ blue.Proxy     = (*sharedTransport)(nil)
)

func newShared(open func(ctx context.Context) (blue.Transport, error)) *sharedTransport {
	return &sharedTransport{open: open, sem: make(chan struct{}, 1)}
}

func (s *sharedTransport) lock(ctx context.Context) error {
	select {
	case s.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *sharedTransport) unlock() { <-s.sem }

// get returns the live inner transport, opening or reopening it as needed.
func (s *sharedTransport) get(ctx context.Context) (blue.Transport, error) {
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.unlock()
	if s.closed {
		return nil, fmt.Errorf("bluetooth transport closed: %w", blue.ErrDisconnected)
	}
	if p, ok := s.inner.(blue.Proxy); ok {
		select {
		case <-p.Done(): // the proxy link is gone: reopen (once, we hold the lock)
			old := s.inner
			s.inner = nil
			go func() { _ = old.Close() }()
		default:
		}
	}
	if s.inner == nil {
		tr, err := s.open(ctx)
		if err != nil {
			return nil, err
		}
		s.inner = tr
	}
	return s.inner, nil
}

func (s *sharedTransport) Scan(ctx context.Context, fn func(blue.Advertisement)) error {
	tr, err := s.get(ctx)
	if err != nil {
		return err
	}
	return tr.Scan(ctx, fn)
}

func (s *sharedTransport) Connect(ctx context.Context, addr blue.Address) (blue.Conn, error) {
	tr, err := s.get(ctx)
	if err != nil {
		return nil, err
	}
	c, err := tr.Connect(ctx, addr)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, blue.ErrNoSlots) || errors.Is(err, blue.ErrNotFound) || errors.Is(err, blue.ErrProxyHeld) || ctx.Err() != nil {
		return nil, err
	}
	p, ok := tr.(blue.Proxy)
	if !ok {
		return nil, err
	}
	select {
	case <-p.Done():
		return nil, err // link lost: the next call reopens
	default:
	}
	pctx, cancel := context.WithTimeout(ctx, proxyProbe)
	defer cancel()
	if st, perr := p.ProxyStatus(pctx); perr == nil && !st.Subscribed {
		return nil, fmt.Errorf("%w: %v", blue.ErrProxyHeld, err)
	}
	return nil, err
}

// Done closes when the current proxy link does; it never closes for a local
// adapter or before the first open.
func (s *sharedTransport) Done() <-chan struct{} {
	select {
	case s.sem <- struct{}{}:
	default:
		return nil // an open is in progress
	}
	defer s.unlock()
	if p, ok := s.inner.(blue.Proxy); ok {
		return p.Done()
	}
	return nil
}

func (s *sharedTransport) ProxyStatus(ctx context.Context) (blue.ProxyStatus, error) {
	if err := s.lock(ctx); err != nil {
		return blue.ProxyStatus{}, err
	}
	inner := s.inner
	s.unlock()
	p, ok := inner.(blue.Proxy)
	if !ok {
		return blue.ProxyStatus{}, errNotProxy
	}
	return p.ProxyStatus(ctx)
}

// Close closes the inner transport, once.
func (s *sharedTransport) Close() error {
	s.sem <- struct{}{}
	defer s.unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.inner == nil {
		return nil
	}
	err := s.inner.Close()
	s.inner = nil
	return err
}
