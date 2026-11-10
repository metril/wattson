package store_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

type rideFix struct {
	s          *store.Store
	a, b       store.User
	a1, a2, b1 store.Rider
	route      store.Route
}

func newRideFix(t *testing.T) rideFix {
	t.Helper()
	ctx := t.Context()
	s := storetest.New(t)
	var f rideFix
	var err error
	f.s = s
	mk := func(name string) store.User {
		u, err := s.CreateUser(ctx, store.User{Username: name})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	mr := func(u store.User, name string) store.Rider {
		r, err := s.CreateRider(ctx, store.DefaultRider(u.ID, name))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	f.a, f.b = mk("a"), mk("b")
	f.a1, f.a2, f.b1 = mr(f.a, "A1"), mr(f.a, "A2"), mr(f.b, "B1")
	if f.route, err = s.CreateRoute(ctx, sampleRoute(f.a.ID, "Loop")); err != nil {
		t.Fatal(err)
	}
	return f
}

var t0 = time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)

func mkSamples(from, to int) []store.Sample {
	var out []store.Sample
	for i := from; i < to; i++ {
		out = append(out, store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(i) * 10, SpeedKmh: 36, PowerW: 200, CadenceRpm: 90, Level: 5,
			Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()})
	}
	return out
}

func finished(t *testing.T, ctx context.Context, f rideFix, rid int64) store.Ride {
	t.Helper()
	r, err := f.s.CreateRide(ctx, store.Ride{RiderID: rid, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if r, err = f.s.FinishRide(ctx, r.ID, t0.Add(time.Minute), store.Summary{DurationS: 60}, 0); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRideLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	r, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a1.ID, RouteID: f.route.ID, RouteName: "Loop", Mode: "route", StartedAt: t0})
	if err != nil || r.EndedAt != nil || r.RouteID != f.route.ID || r.Strava.Status != "none" {
		t.Fatalf("create = %+v, %v", r, err)
	}
	if err := f.s.AppendSamples(ctx, r.ID, mkSamples(0, 10)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AppendSamples(ctx, r.ID, mkSamples(10, 20)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AppendSamples(ctx, r.ID, mkSamples(10, 20)); err != nil {
		t.Fatal(err)
	}
	ss, err := f.s.Samples(ctx, r.ID)
	if err != nil || len(ss) != 20 {
		t.Fatalf("samples = %d, %v", len(ss), err)
	}
	for i, x := range ss {
		if x.Seq != i {
			t.Fatalf("seq[%d] = %d", i, x.Seq)
		}
	}
	sum := store.Summary{DurationS: 20, NPW: 318.35, AvgHRBpm: 142}
	got, err := f.s.FinishRide(ctx, r.ID, t0.Add(20*time.Second), sum, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndedAt == nil || !reflect.DeepEqual(got.Summary, sum) || got.SampleCount != 20 || got.UserID != f.a.ID || got.RiderName != "A1" {
		t.Fatalf("finish = %+v", got)
	}
	if _, err := f.s.Ride(ctx, 9999); err == nil {
		t.Error("missing ride: want error")
	}
	if err := f.s.DeleteRide(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if ss, _ := f.s.Samples(ctx, r.ID); len(ss) != 0 {
		t.Errorf("samples after delete = %d", len(ss))
	}
}

func TestSampleNullRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	r, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a1.ID, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	in := store.Sample{Seq: 0, At: t0.Add(1234 * time.Millisecond), Level: 3, Lat: math.NaN(), Lon: 1.5, EleM: 20}
	if err := f.s.AppendSamples(ctx, r.ID, []store.Sample{in}); err != nil {
		t.Fatal(err)
	}
	var latNull, hrNull bool
	if err := f.s.Read.QueryRow(`SELECT lat IS NULL, hr_bpm IS NULL FROM samples WHERE ride_id=?`, r.ID).Scan(&latNull, &hrNull); err != nil || !latNull || !hrNull {
		t.Fatalf("raw null: %v %v %v", latNull, hrNull, err)
	}
	ss, _ := f.s.Samples(ctx, r.ID)
	if len(ss) != 1 || !math.IsNaN(ss[0].Lat) || ss[0].HRBpm != 0 || ss[0].Lon != 1.5 || ss[0].EleM != 20 ||
		!ss[0].At.Equal(in.At) {
		t.Fatalf("read = %+v", ss)
	}
}

func TestListRidesKeyset(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	for range 25 {
		finished(t, ctx, f, f.a1.ID)
	}
	if _, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a1.ID, Mode: "free", StartedAt: t0}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		finished(t, ctx, f, f.b1.ID)
	}
	page, err := f.s.ListRides(ctx, store.RideQuery{UserID: f.a.ID, Limit: 20})
	if err != nil || len(page) != 20 {
		t.Fatalf("page1 = %d, %v", len(page), err)
	}
	for i, r := range page {
		if r.RiderID != f.a1.ID || r.EndedAt == nil || (i > 0 && r.ID >= page[i-1].ID) {
			t.Fatalf("page1[%d] = %+v", i, r)
		}
	}
	p2, err := f.s.ListRides(ctx, store.RideQuery{UserID: f.a.ID, Before: page[19].ID, Limit: 20})
	if err != nil || len(p2) != 5 || p2[0].ID >= page[19].ID {
		t.Fatalf("page2 = %d, %v", len(p2), err)
	}
	all, _ := f.s.ListRides(ctx, store.RideQuery{Limit: 100})
	if len(all) != 28 {
		t.Errorf("all = %d, want 28", len(all))
	}
	none, _ := f.s.ListRides(ctx, store.RideQuery{UserID: f.a.ID, RiderID: f.a2.ID})
	if len(none) != 0 {
		t.Errorf("A2 rides = %d", len(none))
	}
}

func TestRideCascade(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	r, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a1.ID, RouteID: f.route.ID, RouteName: "Loop", Mode: "route", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.AppendSamples(ctx, r.ID, mkSamples(0, 3)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.PutStravaLink(ctx, store.StravaLink{RiderID: f.a1.ID, ClientID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DeleteRoute(ctx, f.route.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Ride(ctx, r.ID)
	if err != nil || got.RouteID != 0 || got.RouteName != "Loop" {
		t.Fatalf("after route delete = %+v, %v", got, err)
	}
	if err := f.s.DeleteRider(ctx, f.a.ID, f.a1.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM rides WHERE rider_id=?`,
		`SELECT count(*) FROM strava_links WHERE rider_id=?`,
	} {
		var n int
		if err := f.s.Read.QueryRow(q, f.a1.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s = %d, %v", q, n, err)
		}
	}
	var n int
	if err := f.s.Read.QueryRow(`SELECT count(*) FROM samples WHERE ride_id=?`, r.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("samples = %d, %v", n, err)
	}
}

func TestClaimStravaUpload(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	r := finished(t, ctx, f, f.a1.ID)
	now := t0.Add(time.Hour)
	claim := func(want bool) {
		t.Helper()
		ok, err := f.s.ClaimStravaUpload(ctx, r.ID, now)
		if err != nil || ok != want {
			t.Fatalf("claim = %v, %v; want %v", ok, err, want)
		}
	}
	claim(true)
	claim(false)
	if err := f.s.SetStravaState(ctx, r.ID, store.StravaState{Status: "error", UploadID: 3, Error: "boom", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claim(true)
	got, _ := f.s.Ride(ctx, r.ID)
	if got.Strava.Error != "" || got.Strava.UploadID != 0 || got.Strava.Status != "uploading" {
		t.Errorf("claim did not clear: %+v", got.Strava)
	}
	if err := f.s.SetStravaState(ctx, r.ID, store.StravaState{Status: "done", ActivityID: 7, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claim(false)
	got, _ = f.s.Ride(ctx, r.ID)
	if got.Strava.ActivityID != 7 || !got.Strava.UpdatedAt.Equal(now) {
		t.Errorf("state = %+v", got.Strava)
	}
}

func TestStravaLinkUpsert(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	z, err := f.s.StravaLink(ctx, f.a1.ID)
	if err != nil || z.RiderID != f.a1.ID || z.Connected() || z.AthleteID != 0 || z.ConnectedAt != nil {
		t.Fatalf("unknown = %+v, %v", z, err)
	}
	conn := t0
	in := store.StravaLink{RiderID: f.a1.ID, ClientID: "cid", ClientSecretEnc: "v1.sec", AthleteID: 42, AthleteName: "Ann",
		Scope: "activity:write", AccessEnc: "v1.acc", RefreshEnc: "v1.ref", ExpiresAt: t0.Add(6 * time.Hour),
		AutoUpload: true, ConnectedAt: &conn}
	if err := f.s.PutStravaLink(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.StravaLink(ctx, f.a1.ID)
	if err != nil || !got.Connected() || got.ConnectedAt == nil || !got.ConnectedAt.Equal(conn) || !got.ExpiresAt.Equal(in.ExpiresAt) {
		t.Fatalf("get = %+v, %v", got, err)
	}
	got.ConnectedAt, in.ConnectedAt = nil, nil
	got.ExpiresAt, in.ExpiresAt = time.Time{}, time.Time{}
	if got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
	in2 := store.StravaLink{RiderID: f.a1.ID, ClientID: "other"}
	if err := f.s.PutStravaLink(ctx, in2); err != nil {
		t.Fatal(err)
	}
	got, _ = f.s.StravaLink(ctx, f.a1.ID)
	if got != in2 {
		t.Fatalf("overwrite = %+v", got)
	}
}

func TestOpenAndUploadingRides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	done := finished(t, ctx, f, f.a1.ID)
	open, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a2.ID, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	up := finished(t, ctx, f, f.b1.ID)
	if ok, err := f.s.ClaimStravaUpload(ctx, up.ID, t0); !ok || err != nil {
		t.Fatal(ok, err)
	}
	ids := func(rs []store.Ride, err error) []int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(f.s.OpenRides(ctx)); !slices.Equal(got, []int64{open.ID}) {
		t.Errorf("open = %v", got)
	}
	if got := ids(f.s.RidesUploading(ctx)); !slices.Equal(got, []int64{up.ID}) {
		t.Errorf("uploading = %v (done=%d)", got, done.ID)
	}
}

func TestFinishRideOnlyOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	r := finished(t, ctx, f, f.a1.ID)
	if _, err := f.s.FinishRide(ctx, r.ID, t0.Add(time.Hour), store.Summary{DurationS: 999}, 5); !errors.Is(err, store.ErrAlreadyFinished) {
		t.Fatalf("second finish err = %v", err)
	}
	got, _ := f.s.Ride(ctx, r.ID)
	if got.Summary.DurationS != 60 || got.EndedAt == nil || !got.EndedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("ride changed: %+v", got)
	}
	if _, err := f.s.FinishRide(ctx, 9999, t0, store.Summary{}, 0); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing ride err = %v", err)
	}
}

func TestClaimStravaUploadNeedsFinished(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newRideFix(t)
	open, err := f.s.CreateRide(ctx, store.Ride{RiderID: f.a1.ID, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := f.s.ClaimStravaUpload(ctx, open.ID, t0); ok || err != nil {
		t.Fatalf("claim open ride = %v, %v", ok, err)
	}
}

func TestCreateRideSnapshots(t *testing.T) {
	t.Parallel()
	f := newRideFix(t)
	ctx := t.Context()
	rd := f.a1
	rd.MaxHRBpm, rd.FTPW = 185, 260
	if _, err := f.s.UpdateRider(ctx, rd); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.CreateRide(ctx, store.Ride{RiderID: rd.ID, RouteID: f.route.ID, Mode: "route", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if r.FTPW != 260 || r.HRMaxBpm != 185 || r.WeightKg != rd.WeightKg || r.RouteDistanceM != f.route.DistanceM {
		t.Errorf("snapshot = %+v (route %v)", r, f.route.DistanceM)
	}
	if _, err := f.s.CreateRide(ctx, store.Ride{RiderID: 9999, Mode: "free", StartedAt: t0}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown rider err = %v", err)
	}
}
