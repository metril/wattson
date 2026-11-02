package ridectl

import (
	"sync"

	"github.com/metril/wattson/internal/engine"
)

// Event is one message on the manager's stream: a status change or a ride tick.
type Event struct {
	Type   string       `json:"type" doc:"Event kind: status or tick."`
	Status *Status      `json:"status,omitempty" doc:"Set when type is status."`
	Tick   *engine.Tick `json:"tick,omitempty" doc:"Set when type is tick."`
}

// hub fans events out to subscribers without ever blocking the publisher: a
// full subscriber loses its oldest event. It never calls back into Manager,
// so it may be used while the manager's lock is held.
type hub struct {
	mu     sync.Mutex
	subs   map[chan Event]struct{}
	closed bool
}

func newHub() *hub { return &hub{subs: map[chan Event]struct{}{}} }

// add registers a subscriber whose channel starts with first (buf is at least
// len(first)).
func (h *hub) add(buf int, first ...Event) (<-chan Event, func()) {
	ch := make(chan Event, max(buf, len(first), 1))
	for _, e := range first {
		ch <- e
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
}

func (h *hub) publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		push(ch, e)
	}
}

// close ends every subscription; later adds return a closed channel.
func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		close(ch)
	}
	clear(h.subs)
}

// push sends v, dropping the oldest buffered value when ch is full. The
// caller is the only sender on ch.
func push[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}
