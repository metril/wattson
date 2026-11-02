package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RoutePhotos is the cached street photo list of a route. Photos holds the
// gzip JSON the mapillary package encodes; the store does not look inside.
type RoutePhotos struct {
	RouteID int64
	Status  string // ready or error
	TokenFP string // fingerprint of the Mapillary token that built it
	BuiltAt time.Time
	Error   string // user-facing reason when Status is error
	Photos  []byte
}

// GetRoutePhotos returns the cached photos of a route, ErrNotFound if none.
func (s *Store) GetRoutePhotos(ctx context.Context, routeID int64) (RoutePhotos, error) {
	var p RoutePhotos
	var built int64
	err := s.Read.QueryRowContext(ctx,
		`SELECT route_id, status, token_fp, built_at, error, photos FROM route_photos WHERE route_id=?`, routeID).
		Scan(&p.RouteID, &p.Status, &p.TokenFP, &built, &p.Error, &p.Photos)
	if errors.Is(err, sql.ErrNoRows) {
		return RoutePhotos{}, ErrNotFound
	}
	if err != nil {
		return RoutePhotos{}, fmt.Errorf("get route photos: %w", err)
	}
	p.BuiltAt = time.Unix(built, 0).UTC()
	return p, nil
}

// PutRoutePhotos stores p, replacing any earlier row. A route deleted in the
// meantime is ErrNotFound.
func (s *Store) PutRoutePhotos(ctx context.Context, p RoutePhotos) error {
	_, err := s.Write.ExecContext(ctx,
		`INSERT INTO route_photos(route_id, status, token_fp, built_at, error, photos) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(route_id) DO UPDATE SET status=excluded.status, token_fp=excluded.token_fp,
		 built_at=excluded.built_at, error=excluded.error, photos=excluded.photos`,
		p.RouteID, p.Status, p.TokenFP, p.BuiltAt.Unix(), p.Error, p.Photos)
	if err != nil {
		if isForeignKey(err) {
			return ErrNotFound
		}
		return fmt.Errorf("put route photos: %w", err)
	}
	return nil
}

// RouteSource returns a route's source (gpx, tcx, fit, draw or synthetic)
// without loading its points, ErrNotFound if it does not exist.
func (s *Store) RouteSource(ctx context.Context, id int64) (string, error) {
	var src string
	err := s.Read.QueryRowContext(ctx, `SELECT source FROM routes WHERE id=?`, id).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get route source: %w", err)
	}
	return src, nil
}

func isForeignKey(err error) bool {
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
