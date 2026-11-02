package ridectl

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/bike"
	"github.com/metril/wattson/internal/blue"
)

func advAddr(b byte) blue.Address { return blue.Address{MAC: [6]byte{0, 0, 0, 0, 0, b}} }

func TestScanDedupAndOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		A, B, C := advAddr(1), advAddr(2), advAddr(3)
		tr := &advTransport{advs: []blue.Advertisement{
			{Addr: A, RSSI: -80, Services: []blue.UUID{bike.FitPlusService}},
			{Addr: A, Name: "Bike", RSSI: -60, Services: []blue.UUID{bike.FitPlusService}},
			{Addr: B, RSSI: -40},
			{Addr: C, RSSI: -70, Services: []blue.UUID{bike.FTMSService}},
		}}
		m := newMgr(nil, 0, nil, Deps{OpenLocal: func() (blue.Transport, error) { return tr, nil }})
		defer m.Close(context.Background())
		got, err := m.Scan(t.Context(), ScanRequest{Duration: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		want := []Device{
			{Address: A.String(), Name: "Bike", RSSI: -60, Protocol: "fitplus"},
			{Address: C.String(), RSSI: -70, Protocol: "ftms"},
			{Address: B.String(), RSSI: -40},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v\nwant %+v", got, want)
		}
		if !tr.closed.Load() {
			t.Error("transport not closed")
		}
	})
}

func TestScanDurationAndOverrides(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tr := &advTransport{}
		var host, key string
		m := newMgr(nil, 0, nil, Deps{OpenESPHome: func(_ context.Context, hp, k string) (blue.Transport, error) {
			host, key = hp, k
			return tr, nil
		}})
		defer m.Close(context.Background())
		t0 := time.Now()
		got, err := m.Scan(t.Context(), ScanRequest{Transport: "esphome", Host: "10.0.0.5", Key: "kk", Duration: time.Hour})
		if err != nil || len(got) != 0 {
			t.Fatalf("Scan = %v, %v", got, err)
		}
		if d := time.Since(t0); d != 30*time.Second {
			t.Errorf("scan took %v, want the 30 s cap", d)
		}
		if host != "10.0.0.5:6053" || key != "kk" {
			t.Errorf("opened %q %q", host, key)
		}
		t0 = time.Now()
		if _, err := m.Scan(t.Context(), ScanRequest{Transport: "esphome", Host: "h:1"}); err != nil {
			t.Fatal(err)
		}
		if d := time.Since(t0); d != 5*time.Second {
			t.Errorf("default scan took %v, want 5 s", d)
		}
	})
}
