package store_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

func TestOIDCProviderCRUD(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	p, err := s.CreateOIDCProvider(ctx, store.OIDCProvider{Slug: "authentik", DisplayName: "Authentik",
		IssuerURL: "https://a.example/application/o/w/", ClientID: "c", ClientSecretEnc: "v1.x",
		Scopes: "openid", AllowedGroups: []string{"riders"}, Enabled: true})
	if err != nil || p.ID == 0 || p.UpdatedAt.IsZero() {
		t.Fatalf("create: %+v, %v", p, err)
	}
	if _, err := s.CreateOIDCProvider(ctx, store.OIDCProvider{Slug: "authentik", DisplayName: "x"}); !errors.Is(err, store.ErrNameConflict) {
		t.Fatalf("duplicate slug err = %v", err)
	}
	got, err := s.OIDCProviderBySlug(ctx, "authentik")
	if err != nil || got.ID != p.ID || !slices.Equal(got.AllowedGroups, []string{"riders"}) || got.AllowedEmails == nil || !got.Enabled {
		t.Fatalf("by slug: %+v, %v", got, err)
	}
	got.ClientID, got.LinkByEmail = "c2", true
	up, err := s.UpdateOIDCProvider(ctx, got)
	if err != nil || up.ClientID != "c2" || !up.LinkByEmail || !up.UpdatedAt.After(got.UpdatedAt) || up.Slug != "authentik" {
		t.Fatalf("update: %+v, %v", up, err)
	}
	if _, err := s.UpdateOIDCProvider(ctx, store.OIDCProvider{ID: 999}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update missing err = %v", err)
	}
	s.CreateOIDCProvider(ctx, store.OIDCProvider{Slug: "b", DisplayName: "alpha", Enabled: true})
	l, err := s.ListOIDCProviders(ctx)
	if err != nil || len(l) != 2 || l[0].Slug != "b" {
		t.Fatalf("list (display name order): %+v, %v", l, err)
	}
	if n, err := s.DisableAllOIDCProviders(ctx); err != nil || n != 2 {
		t.Fatalf("disable all: %d, %v", n, err)
	}
	if n, _ := s.DisableAllOIDCProviders(ctx); n != 0 {
		t.Fatalf("second disable changed %d", n)
	}
	if err := s.DeleteOIDCProvider(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OIDCProvider(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete err = %v", err)
	}
	if err := s.DeleteOIDCProvider(ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice err = %v", err)
	}
}

func TestOIDCProviderColumnDefaults(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	if _, err := s.Write.ExecContext(ctx, `INSERT INTO oidc_providers(slug, display_name, issuer_url, client_id, created_at, updated_at)
		VALUES('d','D','https://x','c','2026-01-01T00:00:00.000000000Z','2026-01-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	p, err := s.OIDCProviderBySlug(ctx, "d")
	if err != nil || p.Scopes != "openid profile email groups" || p.GroupsClaim != "groups" || !p.AutoCreateUsers || p.LinkByEmail || !p.Enabled {
		t.Fatalf("defaults: %+v, %v", p, err)
	}
}

func TestEmailInUse(t *testing.T) {
	t.Parallel()
	s := storetest.New(t)
	mustUser(t, s, store.User{Username: "a", Email: "A@x.test"})
	for email, want := range map[string]bool{"a@x.test": true, "b@x.test": false, "": false} {
		if got, err := s.EmailInUse(ctx, email); err != nil || got != want {
			t.Errorf("EmailInUse(%q) = %v, %v", email, got, err)
		}
	}
}
