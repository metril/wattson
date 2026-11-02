package esphomeapi

import (
	"bytes"
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestBLEDeviceRequestMarshal(t *testing.T) {
	t.Parallel()
	got := BLEDeviceRequest{Address: 0xAABBCCDDEEFF, RequestType: 4, HasAddressType: true, AddressType: 1}.Marshal()
	want := []byte{0x08, 0xff, 0xdd, 0xf7, 0xe6, 0xbc, 0xd7, 0x2a, 0x10, 0x04, 0x18, 0x01, 0x20, 0x01}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
	var back BLEDeviceRequest
	if err := back.Unmarshal(want); err != nil || back.Address != 0xAABBCCDDEEFF || back.RequestType != 4 || !back.HasAddressType || back.AddressType != 1 {
		t.Fatalf("unmarshal: %+v %v", back, err)
	}
}

// Independent, protowire-built wire bytes (the layout a 1.10 client receives:
// unpacked uuid words, full 128-bit UUIDs).
func uuidWire(b []byte, short uint16, packed bool) []byte {
	hi := uint64(short)<<32 | 0x1000
	lo := uint64(0x800000805F9B34FB)
	if packed {
		var p []byte
		p = protowire.AppendVarint(p, hi)
		p = protowire.AppendVarint(p, lo)
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		return protowire.AppendBytes(b, p)
	}
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, hi)
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	return protowire.AppendVarint(b, lo)
}

func charWire(short uint16, handle, props uint64, packed bool) []byte {
	b := uuidWire(nil, short, packed)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, handle)
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, props)
	// unknown fields of every wire type must be skipped
	b = protowire.AppendTag(b, 99, protowire.Fixed32Type)
	b = protowire.AppendFixed32(b, 7)
	b = protowire.AppendTag(b, 98, protowire.Fixed64Type)
	return protowire.AppendFixed64(b, 7)
}

func TestGATTServicesUnmarshal(t *testing.T) {
	t.Parallel()
	for _, packed := range []bool{false, true} {
		svc := uuidWire(nil, 0xFFF0, packed)
		svc = protowire.AppendTag(svc, 2, protowire.VarintType)
		svc = protowire.AppendVarint(svc, 0x0E)
		svc = protowire.AppendTag(svc, 3, protowire.BytesType)
		svc = protowire.AppendBytes(svc, charWire(0xFFF1, 0x10, 0x10, packed))
		svc = protowire.AppendTag(svc, 3, protowire.BytesType)
		svc = protowire.AppendBytes(svc, charWire(0xFFF2, 0x12, 0x04, packed))
		msg := protowire.AppendTag(nil, 1, protowire.VarintType)
		msg = protowire.AppendVarint(msg, 0xAABBCCDDEEFF)
		msg = protowire.AppendTag(msg, 2, protowire.BytesType)
		msg = protowire.AppendBytes(msg, svc)

		var r GATTGetServicesResponse
		if err := r.Unmarshal(msg); err != nil {
			t.Fatal(err)
		}
		if r.Address != 0xAABBCCDDEEFF || len(r.Services) != 1 {
			t.Fatalf("packed=%v: %+v", packed, r)
		}
		s := r.Services[0]
		if s.Full().String() != "0000fff0-0000-1000-8000-00805f9b34fb" || s.Handle != 0x0E || len(s.Characteristics) != 2 {
			t.Fatalf("service: %+v (%s)", s, s.Full())
		}
		c0, c1 := s.Characteristics[0], s.Characteristics[1]
		if c0.Full().String() != "0000fff1-0000-1000-8000-00805f9b34fb" || c0.Handle != 0x10 || c0.Properties&PropNotify == 0 {
			t.Fatalf("c0: %+v", c0)
		}
		if c1.Full().String() != "0000fff2-0000-1000-8000-00805f9b34fb" || c1.Handle != 0x12 || c1.Properties&PropWriteNoResp == 0 {
			t.Fatalf("c1: %+v", c1)
		}
	}
}

func TestGATTShortUUIDAndMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	in := GATTGetServicesResponse{Address: 0x1122, Services: []GATTService{{
		ShortUUID: 0xFFF0, Handle: 1,
		Characteristics: []GATTCharacteristic{{
			UUID: UUID{0x1111, 0x2222}, Handle: 2, Properties: PropRead,
			Descriptors: []GATTDescriptor{{ShortUUID: 0x2902, Handle: 3}},
		}},
	}}}
	var out GATTGetServicesResponse
	if err := out.Unmarshal(in.Marshal()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in=%+v\nout=%+v", in, out)
	}
	if got := out.Services[0].Full().String(); got != "0000fff0-0000-1000-8000-00805f9b34fb" {
		t.Fatal(got)
	}
	if got := out.Services[0].Characteristics[0].Descriptors[0].Full().String(); got != "00002902-0000-1000-8000-00805f9b34fb" {
		t.Fatal(got)
	}
}

func TestRawAdvertisementsRoundTrip(t *testing.T) {
	t.Parallel()
	in := BLERawAdvertisementsResponse{Advertisements: []BLERawAdvertisement{
		{Address: 0xAABBCCDDEEFF, RSSI: -67, AddressType: 1, Data: []byte{2, 1, 6}},
		{Address: 1, RSSI: 5, Data: []byte{0xFF}},
	}}
	var out BLERawAdvertisementsResponse
	if err := out.Unmarshal(in.Marshal()); err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestSimpleMessagesRoundTrip(t *testing.T) {
	t.Parallel()
	roundTrip := func(name string, in interface{ Marshal() []byte }, out interface{ Unmarshal([]byte) error }) {
		t.Helper()
		if err := out.Unmarshal(in.Marshal()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := reflect.ValueOf(out).Elem().Interface(); !reflect.DeepEqual(got, in) {
			t.Fatalf("%s: in=%+v out=%+v", name, in, got)
		}
	}
	roundTrip("hello", HelloRequest{ClientInfo: "wattson", APIVersionMajor: 1, APIVersionMinor: 10}, &HelloRequest{})
	roundTrip("helloresp", HelloResponse{APIVersionMajor: 1, APIVersionMinor: 13, ServerInfo: "x", Name: "n"}, &HelloResponse{})
	roundTrip("devinfo", DeviceInfoResponse{Name: "a", MACAddress: "m", ESPHomeVersion: "v", BTProxyFeatureFlags: 38, APIEncryptionSupport: true}, &DeviceInfoResponse{})
	roundTrip("conn", BLEDeviceConnectionResponse{Address: 5, Connected: true, MTU: 247, Error: -1}, &BLEDeviceConnectionResponse{})
	roundTrip("read", GATTReadRequest{Address: 5, Handle: 9}, &GATTReadRequest{})
	roundTrip("readresp", GATTReadResponse{Address: 5, Handle: 9, Data: []byte{1, 2}}, &GATTReadResponse{})
	roundTrip("write", GATTWriteRequest{Address: 5, Handle: 9, Response: true, Data: []byte{1}}, &GATTWriteRequest{})
	roundTrip("notify", GATTNotifyRequest{Address: 5, Handle: 9, Enable: true}, &GATTNotifyRequest{})
	roundTrip("err", GATTErrorResponse{Address: 5, Handle: 9, Error: -3}, &GATTErrorResponse{})
	roundTrip("free", BLEConnectionsFreeResponse{Free: 2, Limit: 3, Allocated: []uint64{0xAABB, 0xCC}}, &BLEConnectionsFreeResponse{})
	roundTrip("subadv", SubscribeBLEAdvertisementsRequest{Flags: 1}, &SubscribeBLEAdvertisementsRequest{})
	roundTrip("disc", DisconnectRequest{Reason: 1}, &DisconnectRequest{})
}

func TestUnmarshalPackedAllocatedAndErrors(t *testing.T) {
	t.Parallel()
	var p []byte
	p = protowire.AppendVarint(p, 10)
	p = protowire.AppendVarint(p, 20)
	b := protowire.AppendTag(nil, 3, protowire.BytesType)
	b = protowire.AppendBytes(b, p)
	var m BLEConnectionsFreeResponse
	if err := m.Unmarshal(b); err != nil || !reflect.DeepEqual(m.Allocated, []uint64{10, 20}) {
		t.Fatalf("%+v %v", m, err)
	}
	if err := m.Unmarshal([]byte{0x0a, 0x05, 0x01}); err == nil {
		t.Fatal("expected truncation error")
	}
}
