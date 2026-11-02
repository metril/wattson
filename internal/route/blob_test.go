package route

import (
	"encoding/json"
	"math"
	"testing"
)

func TestBlobRoundTrip(t *testing.T) {
	t.Parallel()
	p := mustProcess(t, loadRamp(t))
	b := Encode(p)
	if string(b[:4]) != "WRP1" {
		t.Fatalf("magic = %q", b[:4])
	}
	q, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if q.Len() != p.Len() || q.StepM != p.StepM || q.LengthM != p.LengthM {
		t.Fatalf("shape: %d %v %v", q.Len(), q.StepM, q.LengthM)
	}
	for i := 0; i < p.Len(); i++ {
		near(t, "lat", q.Lat[i], p.Lat[i], 1e-7)
		near(t, "lon", q.Lon[i], p.Lon[i], 1e-7)
		near(t, "ele", q.Ele[i], p.Ele[i], 0.01)
		near(t, "grade", q.Grade[i], p.Grade[i], 0.01)
	}
}

func TestBlobDecodeErrors(t *testing.T) {
	t.Parallel()
	good := Encode(mustProcess(t, loadRamp(t)))
	bad := append([]byte("WRP2"), good[4:]...)
	for name, b := range map[string][]byte{
		"wrong version": bad,
		"empty":         nil,
		"short magic":   []byte("WR"),
		"magic only":    []byte("WRP1"),
		"truncated":     good[:len(good)/2],
		"garbage":       append([]byte("WRP1"), 0xff, 0xff, 0xff, 0x01, 0x02),
	} {
		if _, err := Decode(b); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBlobDecodeRejectsInconsistent(t *testing.T) {
	t.Parallel()
	p := mustProcess(t, loadRamp(t))
	bad := *p
	bad.StepM = 5
	if _, err := Decode(Encode(&bad)); err == nil {
		t.Error("step != StepM accepted")
	}
	bad = *p
	bad.LengthM = p.LengthM + 500 // n no longer matches the length
	if _, err := Decode(Encode(&bad)); err == nil {
		t.Error("length inconsistent with n accepted")
	}
	bad = *p
	bad.LengthM = math.NaN()
	if _, err := Decode(Encode(&bad)); err == nil {
		t.Error("NaN length accepted")
	}
}

func TestBlob100km(t *testing.T) {
	t.Parallel()
	pts := synthTrack(20000, 5, 1)
	p := mustProcess(t, pts)
	b := Encode(p)
	if len(b) >= 100_000 {
		t.Errorf("blob = %d bytes", len(b))
	}
	q, err := Decode(b)
	if err != nil || q.Len() != p.Len() {
		t.Fatalf("decode: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(Encode(mustProcess(f, loadRamp(f))))
	f.Add([]byte("WRP1"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if p, err := Decode(b); err == nil {
			if p.Len() < 2 || len(p.Lon) != p.Len() || len(p.Ele) != p.Len() || len(p.Grade) != p.Len() || math.IsNaN(p.LengthM) {
				t.Fatalf("inconsistent profile accepted")
			}
		}
	})
}

func TestGeoJSONSimplifies(t *testing.T) {
	t.Parallel()
	p := mustProcess(t, loadRamp(t))
	ls := GeoJSON(p)
	if ls.Type != "LineString" {
		t.Errorf("Type = %q", ls.Type)
	}
	want := [][2]float64{{7, 45}, {7, 45.009}}
	if len(ls.Coordinates) != 2 || ls.Coordinates[0] != want[0] || ls.Coordinates[1] != want[1] {
		t.Fatalf("coords = %v", ls.Coordinates)
	}
	if b, err := json.Marshal(ls); err != nil || string(b) != `{"type":"LineString","coordinates":[[7,45],[7,45.009]]}` {
		t.Errorf("json = %s, %v", b, err)
	}
}

func TestGeoJSONKeepsCorners(t *testing.T) {
	t.Parallel()
	// An L-shaped route: north 500 m, then east 500 m.
	var pts []Point
	for i := 0; i <= 50; i++ {
		pts = append(pts, Point{float64(i) * 10 / 111195.08, 0, 100})
	}
	for i := 1; i <= 50; i++ {
		pts = append(pts, Point{500 / 111195.08, float64(i) * 10 / 111195.08, 100})
	}
	ls := GeoJSON(mustProcess(t, pts))
	if len(ls.Coordinates) != 3 {
		t.Fatalf("want 3 vertices, got %d: %v", len(ls.Coordinates), ls.Coordinates)
	}
}
