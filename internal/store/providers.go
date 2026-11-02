package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// OIDCProvider is a row of oidc_providers. IssuerURL holds the discovery
// URL as the admin entered it; ClientSecretEnc is the sealed client secret.
type OIDCProvider struct {
	ID                                    int64
	Slug, DisplayName, IssuerURL          string
	ClientID, ClientSecretEnc             string
	Scopes, GroupsClaim                   string
	AllowedGroups, AllowedEmails          []string
	AdminGroup                            string
	AutoCreateUsers, LinkByEmail, Enabled bool
	CreatedAt, UpdatedAt                  time.Time
}

const providerCols = `id, slug, display_name, issuer_url, client_id, client_secret_enc, scopes, groups_claim,
	allowed_groups, allowed_emails, admin_group, auto_create_users, link_by_email, enabled, created_at, updated_at`

func scanProvider(r scanner) (OIDCProvider, error) {
	var p OIDCProvider
	var groups, emails, created, updated string
	if err := r.Scan(&p.ID, &p.Slug, &p.DisplayName, &p.IssuerURL, &p.ClientID, &p.ClientSecretEnc, &p.Scopes,
		&p.GroupsClaim, &groups, &emails, &p.AdminGroup, &p.AutoCreateUsers, &p.LinkByEmail, &p.Enabled,
		&created, &updated); err != nil {
		return OIDCProvider{}, err
	}
	if err := json.Unmarshal([]byte(groups), &p.AllowedGroups); err != nil {
		return OIDCProvider{}, fmt.Errorf("parse allowed_groups: %w", err)
	}
	if err := json.Unmarshal([]byte(emails), &p.AllowedEmails); err != nil {
		return OIDCProvider{}, fmt.Errorf("parse allowed_emails: %w", err)
	}
	var err error
	if p.CreatedAt, err = time.Parse(TimeFormat, created); err != nil {
		return OIDCProvider{}, fmt.Errorf("parse created_at: %w", err)
	}
	if p.UpdatedAt, err = time.Parse(TimeFormat, updated); err != nil {
		return OIDCProvider{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return p, nil
}

func jsonList(l []string) string {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l) // strings only; cannot fail
	return string(b)
}

// CreateOIDCProvider inserts p (ID and timestamps are assigned) and returns
// it. ErrNameConflict if the slug exists.
func (s *Store) CreateOIDCProvider(ctx context.Context, p OIDCProvider) (OIDCProvider, error) {
	now := time.Now().UTC()
	res, err := s.Write.ExecContext(ctx,
		`INSERT INTO oidc_providers(slug, display_name, issuer_url, client_id, client_secret_enc, scopes,
			groups_claim, allowed_groups, allowed_emails, admin_group, auto_create_users, link_by_email,
			enabled, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Slug, p.DisplayName, p.IssuerURL, p.ClientID, p.ClientSecretEnc, p.Scopes, p.GroupsClaim,
		jsonList(p.AllowedGroups), jsonList(p.AllowedEmails), p.AdminGroup, p.AutoCreateUsers, p.LinkByEmail,
		p.Enabled, fmtTime(now), fmtTime(now))
	if err != nil {
		if isUnique(err) {
			return OIDCProvider{}, ErrNameConflict
		}
		return OIDCProvider{}, fmt.Errorf("create oidc provider: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return OIDCProvider{}, fmt.Errorf("create oidc provider: %w", err)
	}
	p.ID, p.CreatedAt, p.UpdatedAt = id, now, now
	if p.AllowedGroups == nil {
		p.AllowedGroups = []string{}
	}
	if p.AllowedEmails == nil {
		p.AllowedEmails = []string{}
	}
	return p, nil
}

// UpdateOIDCProvider overwrites every field of the provider p.ID except its
// slug and creation time, and bumps UpdatedAt. ErrNotFound if it is gone.
func (s *Store) UpdateOIDCProvider(ctx context.Context, p OIDCProvider) (OIDCProvider, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE oidc_providers SET display_name=?, issuer_url=?, client_id=?, client_secret_enc=?, scopes=?,
			groups_claim=?, allowed_groups=?, allowed_emails=?, admin_group=?, auto_create_users=?,
			link_by_email=?, enabled=?, updated_at=?
		 WHERE id=?`,
		p.DisplayName, p.IssuerURL, p.ClientID, p.ClientSecretEnc, p.Scopes, p.GroupsClaim,
		jsonList(p.AllowedGroups), jsonList(p.AllowedEmails), p.AdminGroup, p.AutoCreateUsers, p.LinkByEmail,
		p.Enabled, fmtTime(time.Now().UTC()), p.ID)
	if err != nil {
		return OIDCProvider{}, fmt.Errorf("update oidc provider: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return OIDCProvider{}, ErrNotFound
	}
	return s.OIDCProvider(ctx, p.ID)
}

// DeleteOIDCProvider removes the provider; ErrNotFound if it is absent.
func (s *Store) DeleteOIDCProvider(ctx context.Context, id int64) error {
	if err := s.execOne(ctx, `DELETE FROM oidc_providers WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete oidc provider: %w", err)
	}
	return nil
}

func (s *Store) oneProvider(ctx context.Context, where string, arg any) (OIDCProvider, error) {
	p, err := scanProvider(s.Read.QueryRowContext(ctx, `SELECT `+providerCols+` FROM oidc_providers WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return OIDCProvider{}, ErrNotFound
	}
	if err != nil {
		return OIDCProvider{}, fmt.Errorf("get oidc provider: %w", err)
	}
	return p, nil
}

// OIDCProvider returns the provider with id, or ErrNotFound.
func (s *Store) OIDCProvider(ctx context.Context, id int64) (OIDCProvider, error) {
	return s.oneProvider(ctx, `id = ?`, id)
}

// OIDCProviderBySlug returns the provider with slug, or ErrNotFound.
func (s *Store) OIDCProviderBySlug(ctx context.Context, slug string) (OIDCProvider, error) {
	return s.oneProvider(ctx, `slug = ?`, slug)
}

// ListOIDCProviders returns every provider ordered by display name
// (case-insensitive), then ID.
func (s *Store) ListOIDCProviders(ctx context.Context) ([]OIDCProvider, error) {
	rows, err := s.Read.QueryContext(ctx,
		`SELECT `+providerCols+` FROM oidc_providers ORDER BY display_name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list oidc providers: %w", err)
	}
	defer rows.Close()
	var out []OIDCProvider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, fmt.Errorf("scan oidc provider: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list oidc providers: %w", err)
	}
	return out, nil
}

// DisableAllOIDCProviders disables every enabled provider (a lock-out
// recovery switch) and returns how many it changed.
func (s *Store) DisableAllOIDCProviders(ctx context.Context) (int64, error) {
	res, err := s.Write.ExecContext(ctx,
		`UPDATE oidc_providers SET enabled = 0, updated_at = ? WHERE enabled = 1`, fmtTime(time.Now().UTC()))
	if err != nil {
		return 0, fmt.Errorf("disable oidc providers: %w", err)
	}
	return res.RowsAffected()
}
