package esphomeapi

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// Message type IDs of the ESPHome native API (api.proto, esphome 2026.10.1).
const (
	MsgHelloRequest                 uint16 = 1
	MsgHelloResponse                uint16 = 2
	MsgDisconnectRequest            uint16 = 5
	MsgDisconnectResponse           uint16 = 6
	MsgPingRequest                  uint16 = 7
	MsgPingResponse                 uint16 = 8
	MsgDeviceInfoRequest            uint16 = 9
	MsgDeviceInfoResponse           uint16 = 10
	MsgSubscribeBLEAdvertisements   uint16 = 66
	MsgBLEDeviceRequest             uint16 = 68
	MsgBLEDeviceConnectionResponse  uint16 = 69
	MsgGATTGetServicesRequest       uint16 = 70
	MsgGATTGetServicesResponse      uint16 = 71
	MsgGATTGetServicesDoneResponse  uint16 = 72
	MsgGATTReadRequest              uint16 = 73
	MsgGATTReadResponse             uint16 = 74
	MsgGATTWriteRequest             uint16 = 75
	MsgGATTNotifyRequest            uint16 = 78
	MsgGATTNotifyDataResponse       uint16 = 79
	MsgSubscribeBLEConnectionsFree  uint16 = 80
	MsgBLEConnectionsFreeResponse   uint16 = 81
	MsgGATTErrorResponse            uint16 = 82
	MsgGATTWriteResponse            uint16 = 83
	MsgGATTNotifyResponse           uint16 = 84
	MsgUnsubscribeBLEAdvertisements uint16 = 87
	MsgBLERawAdvertisementsResponse uint16 = 93
)

// BluetoothDeviceRequestType values (api.proto).
const (
	BLEReqDisconnect          uint32 = 1 // DISCONNECT
	BLEReqPair                uint32 = 2
	BLEReqUnpair              uint32 = 3
	BLEReqConnectWithCache    uint32 = 4 // CONNECT_V3_WITH_CACHE
	BLEReqConnectWithoutCache uint32 = 5 // CONNECT_V3_WITHOUT_CACHE
	BLEReqClearCache          uint32 = 6
)

// Bluetooth proxy feature flag bits reported in DeviceInfo.BTProxyFlags.
const (
	BTFlagPassiveScan       uint32 = 1 << 0
	BTFlagActiveConnections uint32 = 1 << 1
	BTFlagRemoteCaching     uint32 = 1 << 2
	BTFlagPairing           uint32 = 1 << 3
	BTFlagCacheClearing     uint32 = 1 << 4
	BTFlagRawAdvertisements uint32 = 1 << 5
	BTFlagStateAndMode      uint32 = 1 << 6
)

// ---- wire helpers ----

func putVarint(b []byte, n protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	return putVarintAlways(b, n, v)
}

func putVarintAlways(b []byte, n protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, n, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func putBool(b []byte, n protowire.Number, v bool) []byte {
	if !v {
		return b
	}
	return putVarintAlways(b, n, 1)
}

func putBytes(b []byte, n protowire.Number, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	return putBytesAlways(b, n, v)
}

func putBytesAlways(b []byte, n protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func putString(b []byte, n protowire.Number, v string) []byte {
	return putBytes(b, n, []byte(v))
}

// putInt32 encodes a (possibly negative) int32 as protobuf does: sign-extended varint.
func putInt32(b []byte, n protowire.Number, v int32) []byte {
	return putVarint(b, n, uint64(int64(v)))
}

// field is one decoded top-level field. Only varint and length-delimited
// values are surfaced; other wire types are skipped.
type field struct {
	num protowire.Number
	typ protowire.Type
	v   uint64
	b   []byte
}

func (f field) u32() uint32 {
	if f.typ != protowire.VarintType {
		return 0
	}
	return uint32(f.v)
}

func (f field) u64() uint64 {
	if f.typ != protowire.VarintType {
		return 0
	}
	return f.v
}

func (f field) i32() int32 { return int32(f.u64()) }

func (f field) boolean() bool { return f.typ == protowire.VarintType && f.v != 0 }

func (f field) str() string {
	if f.typ != protowire.BytesType {
		return ""
	}
	return string(f.b)
}

func (f field) raw() []byte {
	if f.typ != protowire.BytesType {
		return nil
	}
	return append([]byte(nil), f.b...)
}

// appendUints appends a repeated varint field, accepting both the unpacked
// (one tag per element) and packed (length-delimited) encodings.
func (f field) appendUints(dst []uint64) ([]uint64, error) {
	switch f.typ {
	case protowire.VarintType:
		return append(dst, f.v), nil
	case protowire.BytesType:
		b := f.b
		for len(b) > 0 {
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return dst, protowire.ParseError(n)
			}
			dst = append(dst, v)
			b = b[n:]
		}
	}
	return dst, nil
}

// eachField walks the fields of an encoded message, skipping unknown wire
// types. fn handles the fields it knows and ignores the rest.
func eachField(b []byte, fn func(f field) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		f := field{num: num, typ: typ}
		switch typ {
		case protowire.VarintType:
			f.v, n = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			f.b, n = protowire.ConsumeBytes(b)
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
		}
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		if typ == protowire.VarintType || typ == protowire.BytesType {
			if err := fn(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- common ----

// Empty is a message with no fields (Ping, DeviceInfoRequest, ...).
type Empty struct{}

func (Empty) Marshal() []byte         { return nil }
func (*Empty) Unmarshal([]byte) error { return nil }

// ---- hello / disconnect / device info ----

type HelloRequest struct {
	ClientInfo      string
	APIVersionMajor uint32
	APIVersionMinor uint32
}

func (m HelloRequest) Marshal() []byte {
	var b []byte
	b = putString(b, 1, m.ClientInfo)
	b = putVarint(b, 2, uint64(m.APIVersionMajor))
	return putVarint(b, 3, uint64(m.APIVersionMinor))
}

func (m *HelloRequest) Unmarshal(b []byte) error {
	*m = HelloRequest{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.ClientInfo = f.str()
		case 2:
			m.APIVersionMajor = f.u32()
		case 3:
			m.APIVersionMinor = f.u32()
		}
		return nil
	})
}

type HelloResponse struct {
	APIVersionMajor uint32
	APIVersionMinor uint32
	ServerInfo      string
	Name            string
}

func (m HelloResponse) Marshal() []byte {
	var b []byte
	b = putVarint(b, 1, uint64(m.APIVersionMajor))
	b = putVarint(b, 2, uint64(m.APIVersionMinor))
	b = putString(b, 3, m.ServerInfo)
	return putString(b, 4, m.Name)
}

func (m *HelloResponse) Unmarshal(b []byte) error {
	*m = HelloResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.APIVersionMajor = f.u32()
		case 2:
			m.APIVersionMinor = f.u32()
		case 3:
			m.ServerInfo = f.str()
		case 4:
			m.Name = f.str()
		}
		return nil
	})
}

type DisconnectRequest struct{ Reason uint32 }

func (m DisconnectRequest) Marshal() []byte { return putVarint(nil, 1, uint64(m.Reason)) }

func (m *DisconnectRequest) Unmarshal(b []byte) error {
	*m = DisconnectRequest{}
	return eachField(b, func(f field) error {
		if f.num == 1 {
			m.Reason = f.u32()
		}
		return nil
	})
}

// DeviceInfoResponse carries the subset of fields wattson uses.
type DeviceInfoResponse struct {
	Name                 string // 2
	MACAddress           string // 3
	ESPHomeVersion       string // 4
	CompilationTime      string // 5
	Model                string // 6
	Manufacturer         string // 12
	FriendlyName         string // 13
	BTProxyFeatureFlags  uint32 // 15
	BluetoothMACAddress  string // 18
	APIEncryptionSupport bool   // 19
}

func (m DeviceInfoResponse) Marshal() []byte {
	var b []byte
	b = putString(b, 2, m.Name)
	b = putString(b, 3, m.MACAddress)
	b = putString(b, 4, m.ESPHomeVersion)
	b = putString(b, 5, m.CompilationTime)
	b = putString(b, 6, m.Model)
	b = putString(b, 12, m.Manufacturer)
	b = putString(b, 13, m.FriendlyName)
	b = putVarint(b, 15, uint64(m.BTProxyFeatureFlags))
	b = putString(b, 18, m.BluetoothMACAddress)
	return putBool(b, 19, m.APIEncryptionSupport)
}

func (m *DeviceInfoResponse) Unmarshal(b []byte) error {
	*m = DeviceInfoResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 2:
			m.Name = f.str()
		case 3:
			m.MACAddress = f.str()
		case 4:
			m.ESPHomeVersion = f.str()
		case 5:
			m.CompilationTime = f.str()
		case 6:
			m.Model = f.str()
		case 12:
			m.Manufacturer = f.str()
		case 13:
			m.FriendlyName = f.str()
		case 15:
			m.BTProxyFeatureFlags = f.u32()
		case 18:
			m.BluetoothMACAddress = f.str()
		case 19:
			m.APIEncryptionSupport = f.boolean()
		}
		return nil
	})
}

// ---- bluetooth: advertisements ----

type SubscribeBLEAdvertisementsRequest struct{ Flags uint32 }

func (m SubscribeBLEAdvertisementsRequest) Marshal() []byte {
	return putVarint(nil, 1, uint64(m.Flags))
}

func (m *SubscribeBLEAdvertisementsRequest) Unmarshal(b []byte) error {
	*m = SubscribeBLEAdvertisementsRequest{}
	return eachField(b, func(f field) error {
		if f.num == 1 {
			m.Flags = f.u32()
		}
		return nil
	})
}

// BLERawAdvertisement is one advertisement report.
type BLERawAdvertisement struct {
	Address     uint64 // 1
	RSSI        int32  // 2, sint32
	AddressType uint32 // 3
	Data        []byte // 4
}

func (m BLERawAdvertisement) Marshal() []byte {
	var b []byte
	b = putVarint(b, 1, m.Address)
	b = putVarint(b, 2, protowire.EncodeZigZag(int64(m.RSSI)))
	b = putVarint(b, 3, uint64(m.AddressType))
	return putBytes(b, 4, m.Data)
}

func (m *BLERawAdvertisement) Unmarshal(b []byte) error {
	*m = BLERawAdvertisement{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.RSSI = int32(protowire.DecodeZigZag(f.u64()))
		case 3:
			m.AddressType = f.u32()
		case 4:
			m.Data = f.raw()
		}
		return nil
	})
}

type BLERawAdvertisementsResponse struct{ Advertisements []BLERawAdvertisement }

func (m BLERawAdvertisementsResponse) Marshal() []byte {
	var b []byte
	for _, a := range m.Advertisements {
		b = putBytesAlways(b, 1, a.Marshal())
	}
	return b
}

func (m *BLERawAdvertisementsResponse) Unmarshal(b []byte) error {
	*m = BLERawAdvertisementsResponse{}
	return eachField(b, func(f field) error {
		if f.num == 1 && f.typ == protowire.BytesType {
			var a BLERawAdvertisement
			if err := a.Unmarshal(f.b); err != nil {
				return err
			}
			m.Advertisements = append(m.Advertisements, a)
		}
		return nil
	})
}

// ---- bluetooth: connection ----

type BLEDeviceRequest struct {
	Address        uint64 // 1
	RequestType    uint32 // 2
	HasAddressType bool   // 3
	AddressType    uint32 // 4
}

func (m BLEDeviceRequest) Marshal() []byte {
	var b []byte
	b = putVarint(b, 1, m.Address)
	b = putVarint(b, 2, uint64(m.RequestType))
	b = putBool(b, 3, m.HasAddressType)
	return putVarint(b, 4, uint64(m.AddressType))
}

func (m *BLEDeviceRequest) Unmarshal(b []byte) error {
	*m = BLEDeviceRequest{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.RequestType = f.u32()
		case 3:
			m.HasAddressType = f.boolean()
		case 4:
			m.AddressType = f.u32()
		}
		return nil
	})
}

type BLEDeviceConnectionResponse struct {
	Address   uint64 // 1
	Connected bool   // 2
	MTU       uint32 // 3
	Error     int32  // 4
}

func (m BLEDeviceConnectionResponse) Marshal() []byte {
	var b []byte
	b = putVarint(b, 1, m.Address)
	b = putBool(b, 2, m.Connected)
	b = putVarint(b, 3, uint64(m.MTU))
	return putInt32(b, 4, m.Error)
}

func (m *BLEDeviceConnectionResponse) Unmarshal(b []byte) error {
	*m = BLEDeviceConnectionResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Connected = f.boolean()
		case 3:
			m.MTU = f.u32()
		case 4:
			m.Error = f.i32()
		}
		return nil
	})
}

// ---- bluetooth: GATT services ----

// UUID is a 128-bit UUID as two big-endian words (High, Low).
type UUID [2]uint64

// String formats the UUID in canonical 8-4-4-4-12 form.
func (u UUID) String() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u[0]>>32, (u[0]>>16)&0xFFFF, u[0]&0xFFFF, u[1]>>48, u[1]&0xFFFFFFFFFFFF)
}

// uuidFromWords builds a UUID from the wire's repeated uint64. The firmware
// omits zero words, so missing words are zero.
func uuidFromWords(w []uint64) UUID {
	var u UUID
	copy(u[:], w)
	return u
}

// shortToFull expands a 16/32-bit UUID onto the Bluetooth base UUID.
func shortToFull(short uint32) UUID {
	return UUID{uint64(short)<<32 | 0x00001000, 0x800000805F9B34FB}
}

func resolveUUID(u UUID, short uint32) UUID {
	if short != 0 {
		return shortToFull(short)
	}
	return u
}

func putUUID(b []byte, u UUID) []byte {
	if u == (UUID{}) {
		return b
	}
	b = putVarintAlways(b, 1, u[0])
	return putVarintAlways(b, 1, u[1])
}

type GATTDescriptor struct {
	UUID      UUID   // 1 (repeated uint64: high, low)
	Handle    uint32 // 2
	ShortUUID uint32 // 3 (only sent to clients announcing API >= 1.12)
}

// Full returns the 128-bit UUID whichever encoding the server used.
func (m GATTDescriptor) Full() UUID { return resolveUUID(m.UUID, m.ShortUUID) }

func (m GATTDescriptor) Marshal() []byte {
	b := putUUID(nil, m.UUID)
	b = putVarint(b, 2, uint64(m.Handle))
	return putVarint(b, 3, uint64(m.ShortUUID))
}

func (m *GATTDescriptor) Unmarshal(b []byte) error {
	*m = GATTDescriptor{}
	var words []uint64
	err := eachField(b, func(f field) (err error) {
		switch f.num {
		case 1:
			words, err = f.appendUints(words)
		case 2:
			m.Handle = f.u32()
		case 3:
			m.ShortUUID = f.u32()
		}
		return err
	})
	m.UUID = uuidFromWords(words)
	return err
}

type GATTCharacteristic struct {
	UUID        UUID             // 1
	Handle      uint32           // 2
	Properties  uint32           // 3
	Descriptors []GATTDescriptor // 4
	ShortUUID   uint32           // 5
}

// Characteristic property bits (GATT).
const (
	PropRead        uint32 = 0x02
	PropWriteNoResp uint32 = 0x04
	PropWrite       uint32 = 0x08
	PropNotify      uint32 = 0x10
	PropIndicate    uint32 = 0x20
)

func (m GATTCharacteristic) Full() UUID { return resolveUUID(m.UUID, m.ShortUUID) }

func (m GATTCharacteristic) Marshal() []byte {
	b := putUUID(nil, m.UUID)
	b = putVarint(b, 2, uint64(m.Handle))
	b = putVarint(b, 3, uint64(m.Properties))
	for _, d := range m.Descriptors {
		b = putBytesAlways(b, 4, d.Marshal())
	}
	return putVarint(b, 5, uint64(m.ShortUUID))
}

func (m *GATTCharacteristic) Unmarshal(b []byte) error {
	*m = GATTCharacteristic{}
	var words []uint64
	err := eachField(b, func(f field) (err error) {
		switch f.num {
		case 1:
			words, err = f.appendUints(words)
		case 2:
			m.Handle = f.u32()
		case 3:
			m.Properties = f.u32()
		case 4:
			if f.typ == protowire.BytesType {
				var d GATTDescriptor
				if err = d.Unmarshal(f.b); err == nil {
					m.Descriptors = append(m.Descriptors, d)
				}
			}
		case 5:
			m.ShortUUID = f.u32()
		}
		return err
	})
	m.UUID = uuidFromWords(words)
	return err
}

type GATTService struct {
	UUID            UUID                 // 1
	Handle          uint32               // 2
	Characteristics []GATTCharacteristic // 3
	ShortUUID       uint32               // 4
}

func (m GATTService) Full() UUID { return resolveUUID(m.UUID, m.ShortUUID) }

func (m GATTService) Marshal() []byte {
	b := putUUID(nil, m.UUID)
	b = putVarint(b, 2, uint64(m.Handle))
	for _, c := range m.Characteristics {
		b = putBytesAlways(b, 3, c.Marshal())
	}
	return putVarint(b, 4, uint64(m.ShortUUID))
}

func (m *GATTService) Unmarshal(b []byte) error {
	*m = GATTService{}
	var words []uint64
	err := eachField(b, func(f field) (err error) {
		switch f.num {
		case 1:
			words, err = f.appendUints(words)
		case 2:
			m.Handle = f.u32()
		case 3:
			if f.typ == protowire.BytesType {
				var c GATTCharacteristic
				if err = c.Unmarshal(f.b); err == nil {
					m.Characteristics = append(m.Characteristics, c)
				}
			}
		case 4:
			m.ShortUUID = f.u32()
		}
		return err
	})
	m.UUID = uuidFromWords(words)
	return err
}

type GATTGetServicesRequest struct{ Address uint64 }

func (m GATTGetServicesRequest) Marshal() []byte { return putVarint(nil, 1, m.Address) }

func (m *GATTGetServicesRequest) Unmarshal(b []byte) error {
	*m = GATTGetServicesRequest{}
	return eachField(b, func(f field) error {
		if f.num == 1 {
			m.Address = f.u64()
		}
		return nil
	})
}

// GATTGetServicesDoneResponse has the same shape as the request.
type GATTGetServicesDoneResponse = GATTGetServicesRequest

type GATTGetServicesResponse struct {
	Address  uint64        // 1
	Services []GATTService // 2
}

func (m GATTGetServicesResponse) Marshal() []byte {
	b := putVarint(nil, 1, m.Address)
	for _, s := range m.Services {
		b = putBytesAlways(b, 2, s.Marshal())
	}
	return b
}

func (m *GATTGetServicesResponse) Unmarshal(b []byte) error {
	*m = GATTGetServicesResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			if f.typ == protowire.BytesType {
				var s GATTService
				if err := s.Unmarshal(f.b); err != nil {
					return err
				}
				m.Services = append(m.Services, s)
			}
		}
		return nil
	})
}

// ---- bluetooth: GATT read / write / notify ----

// GATTReadRequest is the {address, handle} pair.
type GATTReadRequest struct {
	Address uint64 // 1
	Handle  uint32 // 2
}

func (m GATTReadRequest) Marshal() []byte {
	return putVarint(putVarint(nil, 1, m.Address), 2, uint64(m.Handle))
}

func (m *GATTReadRequest) Unmarshal(b []byte) error {
	*m = GATTReadRequest{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Handle = f.u32()
		}
		return nil
	})
}

// GATTWriteResponse and GATTNotifyResponse are {address, handle} acks.
type GATTWriteResponse = GATTReadRequest
type GATTNotifyResponse = GATTReadRequest

// GATTReadResponse is also the shape of GATTNotifyDataResponse.
type GATTReadResponse struct {
	Address uint64 // 1
	Handle  uint32 // 2
	Data    []byte // 3
}

type GATTNotifyDataResponse = GATTReadResponse

func (m GATTReadResponse) Marshal() []byte {
	b := putVarint(nil, 1, m.Address)
	b = putVarint(b, 2, uint64(m.Handle))
	return putBytes(b, 3, m.Data)
}

func (m *GATTReadResponse) Unmarshal(b []byte) error {
	*m = GATTReadResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Handle = f.u32()
		case 3:
			m.Data = f.raw()
		}
		return nil
	})
}

type GATTWriteRequest struct {
	Address  uint64 // 1
	Handle   uint32 // 2
	Response bool   // 3
	Data     []byte // 4
}

func (m GATTWriteRequest) Marshal() []byte {
	b := putVarint(nil, 1, m.Address)
	b = putVarint(b, 2, uint64(m.Handle))
	b = putBool(b, 3, m.Response)
	return putBytes(b, 4, m.Data)
}

func (m *GATTWriteRequest) Unmarshal(b []byte) error {
	*m = GATTWriteRequest{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Handle = f.u32()
		case 3:
			m.Response = f.boolean()
		case 4:
			m.Data = f.raw()
		}
		return nil
	})
}

type GATTNotifyRequest struct {
	Address uint64 // 1
	Handle  uint32 // 2
	Enable  bool   // 3
}

func (m GATTNotifyRequest) Marshal() []byte {
	b := putVarint(nil, 1, m.Address)
	b = putVarint(b, 2, uint64(m.Handle))
	return putBool(b, 3, m.Enable)
}

func (m *GATTNotifyRequest) Unmarshal(b []byte) error {
	*m = GATTNotifyRequest{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Handle = f.u32()
		case 3:
			m.Enable = f.boolean()
		}
		return nil
	})
}

type GATTErrorResponse struct {
	Address uint64 // 1
	Handle  uint32 // 2
	Error   int32  // 3
}

func (m GATTErrorResponse) Marshal() []byte {
	b := putVarint(nil, 1, m.Address)
	b = putVarint(b, 2, uint64(m.Handle))
	return putInt32(b, 3, m.Error)
}

func (m *GATTErrorResponse) Unmarshal(b []byte) error {
	*m = GATTErrorResponse{}
	return eachField(b, func(f field) error {
		switch f.num {
		case 1:
			m.Address = f.u64()
		case 2:
			m.Handle = f.u32()
		case 3:
			m.Error = f.i32()
		}
		return nil
	})
}

// ---- bluetooth: connection slots ----

type BLEConnectionsFreeResponse struct {
	Free      uint32   // 1
	Limit     uint32   // 2
	Allocated []uint64 // 3 (repeated uint64)
}

func (m BLEConnectionsFreeResponse) Marshal() []byte {
	b := putVarint(nil, 1, uint64(m.Free))
	b = putVarint(b, 2, uint64(m.Limit))
	for _, a := range m.Allocated {
		b = putVarintAlways(b, 3, a)
	}
	return b
}

func (m *BLEConnectionsFreeResponse) Unmarshal(b []byte) error {
	*m = BLEConnectionsFreeResponse{}
	return eachField(b, func(f field) (err error) {
		switch f.num {
		case 1:
			m.Free = f.u32()
		case 2:
			m.Limit = f.u32()
		case 3:
			m.Allocated, err = f.appendUints(m.Allocated)
		}
		return err
	})
}
