package store_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func sampleRoute(uid int64, name string) store.Route {
	return store.Route{UserID: uid, Name: name, Source: "gpx", DistanceM: 1000, AscentM: 50, DescentM: 5,
		MinGradePct: -1, MaxGradePct: 6, BBox: [4]float64{1, 2, 3, 4},
		GeoJSON: []byte(`{"type":"LineString","coordinates":[]}`), Spec: []byte(`{}`), Points: []byte("WRP1blob")}
}

func TestRouteCRUD(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	in := sampleRoute(u.ID, "Ramp")
	r, err := s.CreateRoute(ctx, in)
	if err != nil || r.ID <= 0 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", r, err)
	}
	got, err := s.Route(ctx, r.ID)
	if err != nil || !bytes.Equal(got.Points, in.Points) || got.BBox != in.BBox || got.UserID != u.ID ||
		string(got.GeoJSON) != string(in.GeoJSON) || got.MaxGradePct != 6 {
		t.Fatalf("get = %+v, %v", got, err)
	}
	time.Sleep(2 * time.Millisecond)
	rn, err := s.RenameRoute(ctx, r.ID, "Renamed")
	if err != nil || rn.Name != "Renamed" || !rn.UpdatedAt.After(r.UpdatedAt) || !rn.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("rename = %+v, %v", rn, err)
	}
	if _, err := s.RenameRoute(ctx, 999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("rename missing: %v", err)
	}
	if err := s.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Route(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
	if err := s.DeleteRoute(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestListRoutesOmitsBlob(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	a, _ := s.CreateRoute(ctx, sampleRoute(u.ID, "first"))
	time.Sleep(2 * time.Millisecond)
	b, _ := s.CreateRoute(ctx, sampleRoute(u.ID, "second"))
	rs, err := s.ListRoutes(ctx)
	if err != nil || len(rs) != 2 || rs[0].ID != b.ID || rs[1].ID != a.ID {
		t.Fatalf("list = %+v, %v", rs, err)
	}
	for _, r := range rs {
		if r.Points != nil || r.GeoJSON != nil || r.Spec != nil || r.DistanceM != 1000 {
			t.Errorf("list row = %+v", r)
		}
	}
}

func TestRouteUserDeleteSetsNull(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	r, _ := s.CreateRoute(ctx, sampleRoute(u.ID, "r"))
	if _, err := s.Write.ExecContext(ctx, `DELETE FROM users WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Route(ctx, r.ID)
	if err != nil || got.UserID != 0 {
		t.Fatalf("after user delete = %+v, %v", got, err)
	}
}

func TestRouteBadSourceRejected(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	in := sampleRoute(0, "r")
	in.Source = "kml"
	if _, err := s.CreateRoute(t.Context(), in); err == nil {
		t.Fatal("bad source accepted")
	}
}

func TestRiderLeadS(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := storetest.New(t)
	u, _ := s.CreateUser(ctx, store.User{Username: "a"})
	d := store.DefaultRider(u.ID, "A")
	if d.LeadS != 4 {
		t.Fatalf("default LeadS = %v", d.LeadS)
	}
	d.LeadS = 6.5
	r, err := s.CreateRider(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Rider(ctx, u.ID, r.ID)
	if err != nil || got.LeadS != 6.5 {
		t.Fatalf("read back = %+v, %v", got, err)
	}
	got.LeadS = 9
	if u2, err := s.UpdateRider(ctx, got); err != nil || u2.LeadS != 9 {
		t.Fatalf("update = %+v, %v", u2, err)
	}
}
