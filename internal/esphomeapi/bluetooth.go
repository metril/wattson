package esphomeapi

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrNoSlots means the proxy has no free BLE connection slot.
	ErrNoSlots = errors.New("esphomeapi: proxy has no free bluetooth connection slots")
	// ErrBLEDisconnected means the BLE link to the peripheral is (or went) down.
	ErrBLEDisconnected = errors.New("esphomeapi: bluetooth device disconnected")
	// ErrBLEConnect means the proxy reported a failed connection attempt.
	ErrBLEConnect = errors.New("esphomeapi: bluetooth connect failed")
)

// GATTErrNotConnected is the GATT error code (ESP_GATT_NOT_CONNECTED) the
// proxy reports for an operation on a device it is not connected to.
const GATTErrNotConnected int32 = -1

// GATTError is a GATT operation failure reported by the proxy.
type GATTError struct {
	Address uint64
	Handle  uint32
	Code    int32
}

func (e *GATTError) Error() string {
	return fmt.Sprintf("esphomeapi: gatt error %d on %012x handle %d", e.Code, e.Address, e.Handle)
}

// RawAdv is one raw advertisement report from the proxy.
type RawAdv struct {
	Address     uint64
	RSSI        int32
	AddressType uint32
	Data        []byte
}

// reply is one inbound message captured by a watch.
type reply struct {
	typ uint16
	p   []byte
}

type watchSpec struct {
	typ      uint16
	byHandle bool // also require the handle (field 2) to match
}

// peekAddrHandle reads fields 1 (address) and 2 (handle) of a Bluetooth message.
func peekAddrHandle(p []byte) (addr uint64, handle uint32) {
	_ = eachField(p, func(f field) error {
		switch f.num {
		case 1:
			addr = f.u64()
		case 2:
			handle = f.u32()
		}
		return nil
	})
	return addr, handle
}

// watch registers handlers (before the caller sends its request) that forward
// matching messages for addr to the returned channel with a non-blocking send,
// so they are safe on the read goroutine. cancel removes them.
func (c *Client) watch(addr uint64, handle uint32, specs ...watchSpec) (<-chan reply, func()) {
	ch := make(chan reply, 8)
	cancels := make([]func(), 0, len(specs))
	for _, sp := range specs {
		cancels = append(cancels, c.On(sp.typ, func(p []byte) {
			a, h := peekAddrHandle(p)
			if a != addr || (sp.byHandle && h != handle) {
				return
			}
			select {
			case ch <- reply{sp.typ, p}:
			default:
			}
		}))
	}
	return ch, func() {
		for _, f := range cancels {
			f()
		}
	}
}

// closedErr is the error for an operation interrupted by the client closing.
func (c *Client) closedErr() error {
	if err := c.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrClosed, err)
	}
	return ErrClosed
}

// await waits on ch for a message of type want. A GATT error reply (82) or a
// disconnect (69 connected=false) for the watched address fails the call.
func (c *Client) await(ctx context.Context, ch <-chan reply, want uint16) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, c.closedErr()
		case r := <-ch:
			switch r.typ {
			case want:
				return r.p, nil
			case MsgGATTErrorResponse:
				var e GATTErrorResponse
				if err := e.Unmarshal(r.p); err != nil {
					return nil, err
				}
				return nil, &GATTError{Address: e.Address, Handle: e.Handle, Code: e.Error}
			case MsgBLEDeviceConnectionResponse:
				var m BLEDeviceConnectionResponse
				if err := m.Unmarshal(r.p); err != nil {
					return nil, err
				}
				if !m.Connected {
					return nil, disconnectedErr(m.Error)
				}
			}
		}
	}
}

func disconnectedErr(code int32) error {
	if code != 0 {
		return fmt.Errorf("%w (error %d)", ErrBLEDisconnected, code)
	}
	return ErrBLEDisconnected
}

type opKey struct {
	addr   uint64
	handle uint32
}

// lockOp serialises request/response operations on one (address, handle): the
// protocol has no request IDs, so concurrent ops there could steal each
// other's acknowledgements.
func (c *Client) lockOp(ctx context.Context, addr uint64, handle uint32) (func(), error) {
	k := opKey{addr, handle}
	c.opMu.Lock()
	sem := c.opSem[k]
	if sem == nil {
		if c.opSem == nil {
			c.opSem = map[opKey]chan struct{}{}
		}
		sem = make(chan struct{}, 1)
		c.opSem[k] = sem
	}
	c.opMu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.closedErr()
	}
}

// SubscribeAdvertisements asks the proxy for raw advertisements (flags=1) and
// calls fn for each one. fn runs on the client's read goroutine: it must not
// block. When ctx is done the handler is removed and an Unsubscribe is sent.
func (c *Client) SubscribeAdvertisements(ctx context.Context, fn func(RawAdv)) error {
	cancel := c.On(MsgBLERawAdvertisementsResponse, func(p []byte) {
		var m BLERawAdvertisementsResponse
		if m.Unmarshal(p) != nil {
			return
		}
		for _, a := range m.Advertisements {
			fn(RawAdv{Address: a.Address, RSSI: a.RSSI, AddressType: a.AddressType, Data: a.Data})
		}
	})
	if err := c.Send(MsgSubscribeBLEAdvertisements, SubscribeBLEAdvertisementsRequest{Flags: 1}); err != nil {
		cancel()
		return err
	}
	context.AfterFunc(ctx, func() {
		cancel()
		_ = c.Send(MsgUnsubscribeBLEAdvertisements, Empty{})
	})
	return nil
}

// ConnectionsFree asks the proxy for its connection-slot counts and the
// addresses holding them. The proxy answers only the client that holds the
// advertisement subscription, so when another client has taken it over (the
// newest subscriber wins) no reply ever comes and the error is ctx's.
func (c *Client) ConnectionsFree(ctx context.Context) (BLEConnectionsFreeResponse, error) {
	ch := make(chan BLEConnectionsFreeResponse, 1)
	cancel := c.On(MsgBLEConnectionsFreeResponse, func(p []byte) {
		var m BLEConnectionsFreeResponse
		if m.Unmarshal(p) != nil {
			return
		}
		select {
		case ch <- m:
		default:
		}
	})
	defer cancel()
	if err := c.Send(MsgSubscribeBLEConnectionsFree, Empty{}); err != nil {
		return BLEConnectionsFreeResponse{}, err
	}
	select {
	case m := <-ch:
		return m, nil
	case <-ctx.Done():
		return BLEConnectionsFreeResponse{}, ctx.Err()
	case <-c.done:
		return BLEConnectionsFreeResponse{}, c.closedErr()
	}
}

// BLEConnect connects to addr (addrType is the ESPHome address type: 0 public,
// 1 random) and returns the negotiated MTU. It returns ErrNoSlots without
// sending a connect request when the proxy reports no free slot.
func (c *Client) BLEConnect(ctx context.Context, addr uint64, addrType uint32) (uint32, error) {
	r, err := c.ConnectionsFree(ctx)
	if err != nil {
		return 0, err
	}
	if r.Free == 0 {
		return 0, ErrNoSlots
	}

	ch, cancel := c.watch(addr, 0, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	req := BLEDeviceRequest{Address: addr, RequestType: BLEReqConnectWithCache, HasAddressType: true, AddressType: addrType}
	if err := c.Send(MsgBLEDeviceRequest, req); err != nil {
		return 0, err
	}
	for {
		select {
		case <-ctx.Done():
			// Don't leave a half-open attempt holding a slot.
			_ = c.Send(MsgBLEDeviceRequest, BLEDeviceRequest{Address: addr, RequestType: BLEReqDisconnect})
			return 0, ctx.Err()
		case <-c.done:
			return 0, c.closedErr()
		case r := <-ch:
			var m BLEDeviceConnectionResponse
			if err := m.Unmarshal(r.p); err != nil {
				return 0, err
			}
			if !m.Connected {
				if m.Error != 0 {
					return 0, fmt.Errorf("%w (error %d)", ErrBLEConnect, m.Error)
				}
				return 0, ErrBLEConnect
			}
			return m.MTU, nil
		}
	}
}

// BLEDisconnect disconnects from addr and waits for the proxy to confirm.
func (c *Client) BLEDisconnect(ctx context.Context, addr uint64) error {
	ch, cancel := c.watch(addr, 0, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	if err := c.Send(MsgBLEDeviceRequest, BLEDeviceRequest{Address: addr, RequestType: BLEReqDisconnect}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.closedErr()
		case r := <-ch:
			var m BLEDeviceConnectionResponse
			if err := m.Unmarshal(r.p); err != nil {
				return err
			}
			if !m.Connected {
				return nil
			}
		}
	}
}

// OnBLEDisconnect calls fn (on the read goroutine; it must not block) each
// time the proxy reports the link to addr down, with an error wrapping
// ErrBLEDisconnected. The returned func removes the handler.
func (c *Client) OnBLEDisconnect(addr uint64, fn func(err error)) (cancel func()) {
	return c.On(MsgBLEDeviceConnectionResponse, func(p []byte) {
		var m BLEDeviceConnectionResponse
		if m.Unmarshal(p) != nil || m.Address != addr || m.Connected {
			return
		}
		fn(disconnectedErr(m.Error))
	})
}

// GATTServices fetches the connected device's GATT table (71 messages until 72).
func (c *Client) GATTServices(ctx context.Context, addr uint64) ([]GATTService, error) {
	var (
		mu   sync.Mutex
		svcs []GATTService
	)
	cancelSvc := c.On(MsgGATTGetServicesResponse, func(p []byte) {
		var m GATTGetServicesResponse
		if m.Unmarshal(p) != nil || m.Address != addr {
			return
		}
		mu.Lock()
		svcs = append(svcs, m.Services...)
		mu.Unlock()
	})
	defer cancelSvc()
	// Not-connected is reported as a GATT error with handle 0.
	ch, cancel := c.watch(addr, 0, watchSpec{typ: MsgGATTGetServicesDoneResponse},
		watchSpec{MsgGATTErrorResponse, true}, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	if err := c.Send(MsgGATTGetServicesRequest, GATTGetServicesRequest{Address: addr}); err != nil {
		return nil, err
	}
	if _, err := c.await(ctx, ch, MsgGATTGetServicesDoneResponse); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]GATTService(nil), svcs...), nil
}

// GATTRead reads the characteristic value at handle.
func (c *Client) GATTRead(ctx context.Context, addr uint64, handle uint32) ([]byte, error) {
	unlock, err := c.lockOp(ctx, addr, handle)
	if err != nil {
		return nil, err
	}
	defer unlock()
	ch, cancel := c.watch(addr, handle,
		watchSpec{MsgGATTReadResponse, true}, watchSpec{MsgGATTErrorResponse, true}, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	if err := c.Send(MsgGATTReadRequest, GATTReadRequest{Address: addr, Handle: handle}); err != nil {
		return nil, err
	}
	p, err := c.await(ctx, ch, MsgGATTReadResponse)
	if err != nil {
		return nil, err
	}
	var m GATTReadResponse
	if err := m.Unmarshal(p); err != nil {
		return nil, err
	}
	return m.Data, nil
}

// GATTWrite writes data to handle. With resp it waits for the write
// acknowledgement (83); without, it returns once the request is sent.
func (c *Client) GATTWrite(ctx context.Context, addr uint64, handle uint32, data []byte, resp bool) error {
	req := GATTWriteRequest{Address: addr, Handle: handle, Response: resp, Data: data}
	if !resp {
		return c.Send(MsgGATTWriteRequest, req)
	}
	unlock, err := c.lockOp(ctx, addr, handle)
	if err != nil {
		return err
	}
	defer unlock()
	ch, cancel := c.watch(addr, handle,
		watchSpec{MsgGATTWriteResponse, true}, watchSpec{MsgGATTErrorResponse, true}, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	if err := c.Send(MsgGATTWriteRequest, req); err != nil {
		return err
	}
	_, err = c.await(ctx, ch, MsgGATTWriteResponse)
	return err
}

// canceler runs a set of cancel funcs once; funcs added afterwards run at once.
type canceler struct {
	mu   sync.Mutex
	fns  []func()
	done bool
}

func (k *canceler) add(f func()) {
	k.mu.Lock()
	if k.done {
		k.mu.Unlock()
		f()
		return
	}
	k.fns = append(k.fns, f)
	k.mu.Unlock()
}

func (k *canceler) run() {
	k.mu.Lock()
	k.done = true
	fns := k.fns
	k.fns = nil
	k.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

// GATTNotify enables notifications for handle and calls fn (on the read
// goroutine; it must not block) with each value. The proxy writes the CCCD
// itself. The handler is removed automatically when the link to addr goes down.
func (c *Client) GATTNotify(ctx context.Context, addr uint64, handle uint32, fn func([]byte)) error {
	unlock, err := c.lockOp(ctx, addr, handle)
	if err != nil {
		return err
	}
	defer unlock()
	k := &canceler{}
	k.add(c.On(MsgGATTNotifyDataResponse, func(p []byte) {
		var m GATTNotifyDataResponse
		if m.Unmarshal(p) != nil || m.Address != addr || m.Handle != handle {
			return
		}
		fn(m.Data)
	}))
	k.add(c.OnBLEDisconnect(addr, func(error) { k.run() }))
	ch, cancel := c.watch(addr, handle,
		watchSpec{MsgGATTNotifyResponse, true}, watchSpec{MsgGATTErrorResponse, true}, watchSpec{typ: MsgBLEDeviceConnectionResponse})
	defer cancel()
	if err := c.Send(MsgGATTNotifyRequest, GATTNotifyRequest{Address: addr, Handle: handle, Enable: true}); err != nil {
		k.run()
		return err
	}
	if _, err := c.await(ctx, ch, MsgGATTNotifyResponse); err != nil {
		k.run()
		return err
	}
	return nil
}
