package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestRoutePhotosRoundTripAndCascade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := storetest.New(t)
	r, err := s.CreateRoute(ctx, store.Route{Name: "r", Source: "gpx", GeoJSON: []byte(`{}`), Points: []byte{1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRoutePhotos(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	if src, err := s.RouteSource(ctx, r.ID); err != nil || src != "gpx" {
		t.Fatalf("source = %q, %v", src, err)
	}
	if _, err := s.RouteSource(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing source: %v", err)
	}
	at := time.Unix(1700000000, 0).UTC()
	in := store.RoutePhotos{RouteID: r.ID, Status: "ready", TokenFP: "ab", BuiltAt: at, Photos: []byte("x")}
	if err := s.PutRoutePhotos(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Status, in.Error, in.Photos = "error", "boom", nil
	if err := s.PutRoutePhotos(ctx, in); err != nil { // replace
		t.Fatal(err)
	}
	got, err := s.GetRoutePhotos(ctx, r.ID)
	if err != nil || got.Status != "error" || got.Error != "boom" || !got.BuiltAt.Equal(at) || got.TokenFP != "ab" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := s.PutRoutePhotos(ctx, store.RoutePhotos{RouteID: 999, Status: "ready", BuiltAt: at}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("put for missing route: %v", err)
	}
	if err := s.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRoutePhotos(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after cascade: %v", err)
	}
}
