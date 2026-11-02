package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StravaLink is a rider's Strava connection. ClientSecretEnc, AccessEnc and
// RefreshEnc hold secretbox-sealed values (SealFor with an AAD such as
// "strava.rider.<id>.refresh_token"); the store never sees plaintext.
type StravaLink struct {
	RiderID                                   int64
	ClientID, ClientSecretEnc                 string
	AthleteID                                 int64
	AthleteName, Scope, AccessEnc, RefreshEnc string
	ExpiresAt                                 time.Time
	AutoUpload                                bool
	ConnectedAt                               *time.Time
}

// Connected reports whether the rider has authorised Strava.
func (l StravaLink) Connected() bool { return l.RefreshEnc != "" }

// StravaLink returns the rider's link; no row yields a zero link carrying
// only RiderID.
func (s *Store) StravaLink(ctx context.Context, riderID int64) (StravaLink, error) {
	l := StravaLink{RiderID: riderID}
	var athlete sql.NullInt64
	var exp, conn sql.NullString
	var auto int
	err := s.Read.QueryRowContext(ctx,
		`SELECT client_id, client_secret_enc, athlete_id, athlete_name, scope, access_enc, refresh_enc,
			expires_at, auto_upload, connected_at FROM strava_links WHERE rider_id=?`, riderID).
		Scan(&l.ClientID, &l.ClientSecretEnc, &athlete, &l.AthleteName, &l.Scope, &l.AccessEnc,
			&l.RefreshEnc, &exp, &auto, &conn)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	if err != nil {
		return StravaLink{}, fmt.Errorf("get strava link: %w", err)
	}
	l.AthleteID, l.AutoUpload = athlete.Int64, auto != 0
	if exp.Valid {
		if l.ExpiresAt, err = time.Parse(TimeFormat, exp.String); err != nil {
			return StravaLink{}, fmt.Errorf("parse expires_at: %w", err)
		}
	}
	if conn.Valid {
		t, err := time.Parse(TimeFormat, conn.String)
		if err != nil {
			return StravaLink{}, fmt.Errorf("parse connected_at: %w", err)
		}
		l.ConnectedAt = &t
	}
	return l, nil
}

// PutStravaLink inserts or overwrites the rider's link.
func (s *Store) PutStravaLink(ctx context.Context, l StravaLink) error {
	var exp, conn any
	if !l.ExpiresAt.IsZero() {
		exp = fmtTime(l.ExpiresAt)
	}
	if l.ConnectedAt != nil {
		conn = fmtTime(*l.ConnectedAt)
	}
	auto := 0
	if l.AutoUpload {
		auto = 1
	}
	if _, err := s.Write.ExecContext(ctx,
		`INSERT INTO strava_links(rider_id, client_id, client_secret_enc, athlete_id, athlete_name, scope,
			access_enc, refresh_enc, expires_at, auto_upload, connected_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(rider_id) DO UPDATE SET client_id=excluded.client_id,
			client_secret_enc=excluded.client_secret_enc, athlete_id=excluded.athlete_id,
			athlete_name=excluded.athlete_name, scope=excluded.scope, access_enc=excluded.access_enc,
			refresh_enc=excluded.refresh_enc, expires_at=excluded.expires_at,
			auto_upload=excluded.auto_upload, connected_at=excluded.connected_at`,
		l.RiderID, l.ClientID, l.ClientSecretEnc, nullInt(l.AthleteID), l.AthleteName, l.Scope,
		l.AccessEnc, l.RefreshEnc, exp, auto, conn); err != nil {
		return fmt.Errorf("put strava link: %w", err)
	}
	return nil
}

// ConnectedSharedStravaRiders lists the riders that are connected to Strava
// through the shared app (no client ID of their own).
func (s *Store) ConnectedSharedStravaRiders(ctx context.Context) ([]int64, error) {
	rows, err := s.Read.QueryContext(ctx, `SELECT rider_id FROM strava_links WHERE refresh_enc != '' AND client_id = '' ORDER BY rider_id`)
	if err != nil {
		return nil, fmt.Errorf("list shared strava riders: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan shared strava rider: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
