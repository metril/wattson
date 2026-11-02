package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	// ImageIDs are the ids of the photos in Photos; PutRoutePhotos records
	// them for HasPhotoImage. Not filled by GetRoutePhotos.
	ImageIDs []string
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

// PutRoutePhotos stores p, replacing any earlier row and the list of image
// ids (ImageIDs) that the image proxy checks. A route deleted in the
// meantime is ErrNotFound.
func (s *Store) PutRoutePhotos(ctx context.Context, p RoutePhotos) error {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("put route photos: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO route_photos(route_id, status, token_fp, built_at, error, photos) VALUES(?,?,?,?,?,?)
		 ON CONFLICT(route_id) DO UPDATE SET status=excluded.status, token_fp=excluded.token_fp,
		 built_at=excluded.built_at, error=excluded.error, photos=excluded.photos`,
		p.RouteID, p.Status, p.TokenFP, p.BuiltAt.Unix(), p.Error, p.Photos)
	if err == nil {
		_, err = tx.ExecContext(ctx, `DELETE FROM route_photo_ids WHERE route_id=?`, p.RouteID)
	}
	for i := 0; err == nil && i < len(p.ImageIDs); i++ {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO route_photo_ids(image_id, route_id) VALUES(?,?)`, p.ImageIDs[i], p.RouteID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if isForeignKey(err) {
			return ErrNotFound
		}
		return fmt.Errorf("put route photos: %w", err)
	}
	return nil
}

// HasPhotoImage reports whether any route's cached photo list contains image id.
func (s *Store) HasPhotoImage(ctx context.Context, id string) (bool, error) {
	var one int
	err := s.Read.QueryRowContext(ctx, `SELECT 1 FROM route_photo_ids WHERE image_id=? LIMIT 1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check photo image: %w", err)
	}
	return true, nil
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

// isForeignKey reports whether err is a SQLite FOREIGN KEY violation.
func isForeignKey(err error) bool {
	var coder interface{ Code() int }
	return errors.As(err, &coder) && coder.Code() == 787 // SQLITE_CONSTRAINT_FOREIGNKEY
}
