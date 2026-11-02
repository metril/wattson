package esphometest

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// Message type IDs (api.proto) used by the simulated bluetooth_proxy.
const (
	tHelloRequest      = 1
	tHelloResponse     = 2
	tDeviceInfoRequest = 9
	tDeviceInfoResp    = 10
	tSubscribeAdv      = 66
	tUnsubscribeAdv    = 87
	tDeviceRequest     = 68
	tDeviceConnection  = 69
	tGetServices       = 70
	tServicesResp      = 71
	tServicesDone      = 72
	tReadReq           = 73
	tReadResp          = 74
	tWriteReq          = 75
	tNotifyReq         = 78
	tNotifyData        = 79
	tSubscribeFree     = 80
	tFreeResp          = 81
	tErrorResp         = 82
	tWriteResp         = 83
	tNotifyResp        = 84
	tRawAdvs           = 93
)

// Error codes the simulated proxy reports in GATT error responses.
const (
	ErrCodeGATT         = 133 // generic GATT failure
	ErrCodeNotConnected = -1  // ESP_GATT_NOT_CONNECTED
)

// RawAdv is one raw advertisement report.
type RawAdv struct {
	Address     uint64
	RSSI        int32
	AddressType uint32
	Data        []byte
}

// GATTChar is a simulated characteristic. UUID is "fff1" (16-bit) or canonical 128-bit.
type GATTChar struct {
	UUID   string
	Handle uint32
	Props  uint32
}

// GATTService is a simulated primary service.
type GATTService struct {
	UUID   string
	Handle uint32
	Chars  []GATTChar
}

// ConnRequest is a BluetoothDeviceRequest as received by the simulated proxy.
type ConnRequest struct {
	Address        uint64
	RequestType    uint32
	HasAddressType bool
	AddressType    uint32
}

type bleDev struct {
	adv       RawAdv
	svcs      []GATTService
	values    map[uint32][]byte
	connected bool
	notifying map[uint32]bool
}

type bleState struct {
	mu            sync.Mutex
	devs          map[uint64]*bleDev
	free          uint32
	limit         uint32
	installd      bool
	requests      []ConnRequest
	writes        map[uint64][][]byte
	unsubs        int
	subs          int
	freeSubs      int
	advFlags      []uint32
	onGetServices func()
	// subscriber is the session that sent the newest SubscribeBluetoothLE-
	// Advertisements (the real proxy's api_connection_); nil when none or
	// after Unsubscribe. Subscribing replaces any previous subscriber.
	subscriber *session
	dropped    int // BLE replies dropped because nobody was subscribed
	flush      sync.Once
}

func (s *Server) ble() *bleState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bleSt == nil {
		s.bleSt = &bleState{devs: map[uint64]*bleDev{}, free: 3, limit: 3, writes: map[uint64][][]byte{}}
	}
	return s.bleSt
}

// subscribed reports whether the current client is the advertisement
// subscriber. st.mu must be held. Mirrors bluetooth_proxy.cpp: every BLE reply
// (device connection, services, read/write/notify acks, notify data, GATT
// errors, connection-free updates) goes only to api_connection_ and is dropped
// when that is unset or belongs to another connection.
func (s *Server) subscribed(st *bleState) bool {
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()
	return cur != nil && st.subscriber == cur
}

// gate returns rs when the current client is the subscriber, else drops them.
// st.mu must be held.
func (s *Server) gate(st *bleState, rs []Reply) []Reply {
	if len(rs) == 0 || s.subscribed(st) {
		return rs
	}
	st.dropped += len(rs)
	return nil
}

// BLEDroppedReplies returns how many BLE replies the simulated proxy dropped
// because the client had not subscribed to advertisements.
func (s *Server) BLEDroppedReplies() int {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.dropped
}

// HandleHello answers Hello and DeviceInfo like a device with the given
// bluetooth_proxy feature flags.
func (s *Server) HandleHello(flags uint32) {
	s.Handle(tHelloRequest, func([]byte) []Reply {
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, 1)
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, 13)
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendString(b, "ESPHome test")
		b = protowire.AppendTag(b, 5, protowire.BytesType)
		b = protowire.AppendString(b, Name)
		return []Reply{{Type: tHelloResponse, Payload: b}}
	})
	s.Handle(tDeviceInfoRequest, func([]byte) []Reply {
		var b []byte
		b = protowire.AppendTag(b, 2, protowire.BytesType)
		b = protowire.AppendString(b, Name)
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendString(b, MAC)
		b = protowire.AppendTag(b, 4, protowire.BytesType)
		b = protowire.AppendString(b, "2026.10.1")
		b = protowire.AppendTag(b, 15, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(flags))
		return []Reply{{Type: tDeviceInfoResp, Payload: b}}
	})
}

// AddBLEDevice adds a simulated peripheral and installs the bluetooth_proxy
// message handlers (idempotent). The device is reported to advertisement
// subscribers and can be connected to with its GATT services.
func (s *Server) AddBLEDevice(adv RawAdv, svcs []GATTService) {
	st := s.ble()
	st.mu.Lock()
	st.devs[adv.Address] = &bleDev{adv: adv, svcs: svcs, values: map[uint32][]byte{}, notifying: map[uint32]bool{}}
	install := !st.installd
	st.installd = true
	st.mu.Unlock()
	if install {
		s.installBLE(st)
	}
}

// SetBLEValue sets the value returned for GATT reads of handle on addr.
func (s *Server) SetBLEValue(addr uint64, handle uint32, v []byte) {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	if d := st.devs[addr]; d != nil {
		d.values[handle] = append([]byte(nil), v...)
	}
}

// SetFreeSlots sets the number of free connection slots reported by the proxy.
func (s *Server) SetFreeSlots(n int) {
	st := s.ble()
	st.mu.Lock()
	st.free = uint32(n)
	if st.limit < st.free {
		st.limit = st.free
	}
	st.mu.Unlock()
}

// BLEConnRequests returns every BluetoothDeviceRequest received, in order.
func (s *Server) BLEConnRequests() []ConnRequest {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]ConnRequest(nil), st.requests...)
}

// BLEWrites returns the GATT write payloads received for addr, in order.
func (s *Server) BLEWrites(addr uint64) [][]byte {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([][]byte, len(st.writes[addr]))
	for i, w := range st.writes[addr] {
		out[i] = append([]byte(nil), w...)
	}
	return out
}

// BLEUnsubscribes returns how many Unsubscribe-advertisements messages arrived.
func (s *Server) BLEUnsubscribes() int {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.unsubs
}

// OnGetServices registers fn to run when a GetServices request arrives, before
// the proxy processes it (e.g. to drop the link mid-connect).
func (s *Server) OnGetServices(fn func()) {
	st := s.ble()
	st.mu.Lock()
	st.onGetServices = fn
	st.mu.Unlock()
}

// BLEAdvFlags returns the flags of every SubscribeBluetoothLEAdvertisements received.
func (s *Server) BLEAdvFlags() []uint32 {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]uint32(nil), st.advFlags...)
}

// BLEConnected reports whether addr currently holds a simulated connection.
func (s *Server) BLEConnected(addr uint64) bool {
	st := s.ble()
	st.mu.Lock()
	defer st.mu.Unlock()
	d := st.devs[addr]
	return d != nil && d.connected
}

// NotifyBLE sends a notification for handle if the client enabled it.
func (s *Server) NotifyBLE(addr uint64, handle uint32, data []byte) {
	st := s.ble()
	st.mu.Lock()
	d := st.devs[addr]
	ok := d != nil && d.connected && d.notifying[handle]
	sub := s.subscribed(st)
	if ok && !sub {
		st.dropped++
	}
	st.mu.Unlock()
	if ok && !sub {
		return
	}
	if !ok {
		s.t.Errorf("esphometest: NotifyBLE(%x, %d): not connected or notify not enabled", addr, handle)
		return
	}
	s.Push(tNotifyData, encAddrHandleData(addr, handle, data))
}

// DropBLE simulates link loss to addr: BluetoothDeviceConnectionResponse
// connected=false followed by an updated ConnectionsFree.
func (s *Server) DropBLE(addr uint64) {
	st := s.ble()
	st.mu.Lock()
	d := st.devs[addr]
	was := d != nil && d.connected
	if was {
		d.connected = false
		d.notifying = map[uint32]bool{}
		st.free++
	}
	free, limit := st.free, st.limit
	sub := s.subscribed(st)
	if !sub {
		st.dropped += 2
	}
	st.mu.Unlock()
	if !was || !sub {
		return
	}
	s.Push(tDeviceConnection, encConn(addr, false, 0, 0))
	s.Push(tFreeResp, encFree(free, limit))
}

func (s *Server) installBLE(st *bleState) {
	s.Handle(tSubscribeAdv, func(p []byte) []Reply {
		var flags uint32
		forEach(p, func(n protowire.Number, v uint64, _ []byte) {
			if n == 1 {
				flags = uint32(v)
			}
		})
		s.mu.Lock()
		cur := s.cur
		s.mu.Unlock()
		st.mu.Lock()
		defer st.mu.Unlock()
		st.subs++
		st.advFlags = append(st.advFlags, flags)
		st.subscriber = cur // newest subscriber wins
		return s.advBatch(st)
	})
	s.Handle(tUnsubscribeAdv, func([]byte) []Reply {
		s.mu.Lock()
		cur := s.cur
		s.mu.Unlock()
		st.mu.Lock()
		st.unsubs++
		if st.subscriber == cur { // only the subscriber can unsubscribe
			st.subscriber = nil
		}
		st.mu.Unlock()
		return nil
	})
	s.startFlush(st)
	s.Handle(tSubscribeFree, func([]byte) []Reply {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.freeSubs++
		return []Reply{{Type: tFreeResp, Payload: encFree(st.free, st.limit)}}
	})
	s.Handle(tDeviceRequest, func(p []byte) []Reply {
		var r ConnRequest
		forEach(p, func(n protowire.Number, v uint64, _ []byte) {
			switch n {
			case 1:
				r.Address = v
			case 2:
				r.RequestType = uint32(v)
			case 3:
				r.HasAddressType = v != 0
			case 4:
				r.AddressType = uint32(v)
			}
		})
		st.mu.Lock()
		defer st.mu.Unlock()
		st.requests = append(st.requests, r)
		d := st.devs[r.Address]
		switch r.RequestType {
		case 4, 5: // connect
			if d == nil || st.free == 0 {
				return nil // a real proxy keeps trying until the client gives up
			}
			if !s.subscribed(st) {
				// loop() tears down connections owned by no subscriber.
				st.dropped += 2
				return nil
			}
			if !d.connected {
				d.connected = true
				st.free--
			}
			return []Reply{
				{Type: tDeviceConnection, Payload: encConn(r.Address, true, 247, 0)},
				{Type: tFreeResp, Payload: encFree(st.free, st.limit)},
			}
		case 1: // disconnect
			if d != nil && d.connected {
				d.connected = false
				d.notifying = map[uint32]bool{}
				st.free++
			}
			return s.gate(st, []Reply{
				{Type: tDeviceConnection, Payload: encConn(r.Address, false, 0, 0)},
				{Type: tFreeResp, Payload: encFree(st.free, st.limit)},
			})
		}
		return nil
	})
	s.Handle(tGetServices, func(p []byte) []Reply {
		var addr uint64
		forEach(p, func(n protowire.Number, v uint64, _ []byte) {
			if n == 1 {
				addr = v
			}
		})
		st.mu.Lock()
		hook := st.onGetServices
		st.mu.Unlock()
		if hook != nil {
			hook() // runs before the request is processed, without locks held
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		d := st.devs[addr]
		if d == nil || !d.connected {
			// Real bluetooth_proxy.cpp: GATT error ESP_GATT_NOT_CONNECTED, handle 0.
			return s.gate(st, []Reply{{Type: tErrorResp, Payload: encErr(addr, 0, ErrCodeNotConnected)}})
		}
		var out []Reply
		for _, sv := range d.svcs {
			var b []byte
			b = protowire.AppendTag(b, 1, protowire.VarintType)
			b = protowire.AppendVarint(b, addr)
			b = protowire.AppendTag(b, 2, protowire.BytesType)
			b = protowire.AppendBytes(b, encService(sv))
			out = append(out, Reply{Type: tServicesResp, Payload: b})
		}
		var b []byte
		b = protowire.AppendTag(b, 1, protowire.VarintType)
		b = protowire.AppendVarint(b, addr)
		return s.gate(st, append(out, Reply{Type: tServicesDone, Payload: b}))
	})
	// gatt looks up a connected device's handle; on failure returns the reply to send.
	gatt := func(addr uint64, handle uint32) (*bleDev, []Reply) {
		d := st.devs[addr]
		if d == nil || !d.connected {
			return nil, s.gate(st, []Reply{{Type: tErrorResp, Payload: encErr(addr, handle, ErrCodeNotConnected)}})
		}
		for _, sv := range d.svcs {
			for _, c := range sv.Chars {
				if c.Handle == handle {
					return d, nil
				}
			}
		}
		return nil, s.gate(st, []Reply{{Type: tErrorResp, Payload: encErr(addr, handle, ErrCodeGATT)}})
	}
	s.Handle(tReadReq, func(p []byte) []Reply {
		addr, handle, _, _ := decAddrHandle(p)
		st.mu.Lock()
		defer st.mu.Unlock()
		d, bad := gatt(addr, handle)
		if d == nil {
			return bad
		}
		v, ok := d.values[handle]
		if !ok {
			return s.gate(st, []Reply{{Type: tErrorResp, Payload: encErr(addr, handle, ErrCodeGATT)}})
		}
		return s.gate(st, []Reply{{Type: tReadResp, Payload: encAddrHandleData(addr, handle, v)}})
	})
	s.Handle(tWriteReq, func(p []byte) []Reply {
		addr, handle, resp, data := decAddrHandle(p)
		st.mu.Lock()
		defer st.mu.Unlock()
		d, bad := gatt(addr, handle)
		if d == nil {
			return bad
		}
		st.writes[addr] = append(st.writes[addr], data)
		if !resp {
			return nil
		}
		return s.gate(st, []Reply{{Type: tWriteResp, Payload: encAddrHandle(addr, handle)}})
	})
	s.Handle(tNotifyReq, func(p []byte) []Reply {
		addr, handle, enable, _ := decAddrHandle(p)
		st.mu.Lock()
		defer st.mu.Unlock()
		d, bad := gatt(addr, handle)
		if d == nil {
			return bad
		}
		// The proxy writes the CCCD itself; the client never does.
		d.notifying[handle] = enable
		return s.gate(st, []Reply{{Type: tNotifyResp, Payload: encAddrHandle(addr, handle)}})
	})
}

// advBatch is one BluetoothLERawAdvertisementsResponse listing every device
// (nil when there are none). st.mu must be held.
func (s *Server) advBatch(st *bleState) []Reply {
	var b []byte
	for _, d := range st.devs {
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, encAdv(d.adv))
	}
	if b == nil {
		return nil
	}
	return []Reply{{Type: tRawAdvs, Payload: b}}
}

// startFlush emulates the proxy's periodic advertisement flush: while a client
// is subscribed, every device keeps being reported (a real proxy reports each
// advertisement it hears), so a scan started later still sees devices.
func (s *Server) startFlush(st *bleState) {
	st.flush.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			t := time.NewTicker(10 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-s.stop:
					return
				case <-t.C:
				}
				s.mu.Lock()
				cur := s.cur
				s.mu.Unlock()
				st.mu.Lock()
				var rs []Reply
				if cur != nil && st.subscriber == cur {
					rs = s.advBatch(st)
				}
				st.mu.Unlock()
				for _, r := range rs {
					if cur.write(r.Type, r.Payload) != nil {
						break
					}
				}
			}
		}()
	})
}

func forEach(b []byte, fn func(n protowire.Number, v uint64, raw []byte)) {
	for len(b) > 0 {
		n, typ, l := protowire.ConsumeTag(b)
		if l < 0 {
			return
		}
		b = b[l:]
		switch typ {
		case protowire.VarintType:
			v, l := protowire.ConsumeVarint(b)
			if l < 0 {
				return
			}
			fn(n, v, nil)
			b = b[l:]
		case protowire.BytesType:
			v, l := protowire.ConsumeBytes(b)
			if l < 0 {
				return
			}
			fn(n, 0, v)
			b = b[l:]
		default:
			l := protowire.ConsumeFieldValue(n, typ, b)
			if l < 0 {
				return
			}
			b = b[l:]
		}
	}
}

// decAddrHandle decodes the shared {address, handle, flag, data} shape of
// read/write/notify requests (field 3 is a bool, field 4 the data).
func decAddrHandle(p []byte) (addr uint64, handle uint32, flag bool, data []byte) {
	forEach(p, func(n protowire.Number, v uint64, raw []byte) {
		switch n {
		case 1:
			addr = v
		case 2:
			handle = uint32(v)
		case 3:
			flag = v != 0
		case 4:
			data = append([]byte(nil), raw...)
		}
	})
	return
}

func encAddrHandle(addr uint64, handle uint32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, addr)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(handle))
}

func encAddrHandleData(addr uint64, handle uint32, data []byte) []byte {
	b := encAddrHandle(addr, handle)
	b = protowire.AppendTag(b, 3, protowire.BytesType)
	return protowire.AppendBytes(b, data)
}

func encErr(addr uint64, handle uint32, code int32) []byte {
	b := encAddrHandle(addr, handle)
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(int64(code)))
}

func encConn(addr uint64, connected bool, mtu uint32, errCode int32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, addr)
	if connected {
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, 1)
	}
	if mtu != 0 {
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(mtu))
	}
	if errCode != 0 {
		b = protowire.AppendTag(b, 4, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(int64(errCode)))
	}
	return b
}

func encFree(free, limit uint32) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(free))
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	return protowire.AppendVarint(b, uint64(limit))
}

func encAdv(a RawAdv) []byte {
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, a.Address)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, protowire.EncodeZigZag(int64(a.RSSI)))
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(a.AddressType))
	b = protowire.AppendTag(b, 4, protowire.BytesType)
	return protowire.AppendBytes(b, a.Data)
}

// uuidWords converts "fff1", an 8-hex 32-bit form, or a canonical UUID to the
// two big-endian 64-bit words the firmware sends (both always present).
func uuidWords(u string) (hi, lo uint64) {
	u = strings.ToLower(strings.ReplaceAll(u, "-", ""))
	if len(u) == 4 || len(u) == 8 {
		u = strings.Repeat("0", 8-len(u)) + u + "00001000800000805f9b34fb"
	}
	if len(u) != 32 {
		panic(fmt.Sprintf("esphometest: bad uuid %q", u))
	}
	hi, err1 := strconv.ParseUint(u[:16], 16, 64)
	lo, err2 := strconv.ParseUint(u[16:], 16, 64)
	if err1 != nil || err2 != nil {
		panic(fmt.Sprintf("esphometest: bad uuid %q", u))
	}
	return hi, lo
}

func appendUUID(b []byte, u string) []byte {
	hi, lo := uuidWords(u)
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, hi)
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	return protowire.AppendVarint(b, lo)
}

func encService(sv GATTService) []byte {
	b := appendUUID(nil, sv.UUID)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(sv.Handle))
	for _, c := range sv.Chars {
		cb := appendUUID(nil, c.UUID)
		cb = protowire.AppendTag(cb, 2, protowire.VarintType)
		cb = protowire.AppendVarint(cb, uint64(c.Handle))
		cb = protowire.AppendTag(cb, 3, protowire.VarintType)
		cb = protowire.AppendVarint(cb, uint64(c.Props))
		if c.Props&0x30 != 0 { // notify/indicate: real GATT has a CCCD descriptor
			db := appendUUID(nil, "2902")
			db = protowire.AppendTag(db, 2, protowire.VarintType)
			db = protowire.AppendVarint(db, uint64(c.Handle+1))
			cb = protowire.AppendTag(cb, 4, protowire.BytesType)
			cb = protowire.AppendBytes(cb, db)
		}
		b = protowire.AppendTag(b, 3, protowire.BytesType)
		b = protowire.AppendBytes(b, cb)
	}
	return b
}
