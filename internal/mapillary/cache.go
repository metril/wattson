package mapillary

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"
)

// EncodePhotos serialises photos as the gzip JSON stored in route_photos.
func EncodePhotos(ps []Photo) ([]byte, error) {
	if ps == nil {
		ps = []Photo{}
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(ps); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// maxPhotosJSON bounds the decoded size of a stored photo list.
const maxPhotosJSON = 32 << 20

// DecodePhotos is the inverse of EncodePhotos. An empty blob is no photos.
func DecodePhotos(b []byte) ([]Photo, error) {
	if len(b) == 0 {
		return []Photo{}, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode photos: %w", err)
	}
	defer zr.Close()
	var ps []Photo
	if err := json.NewDecoder(io.LimitReader(zr, maxPhotosJSON)).Decode(&ps); err != nil {
		return nil, fmt.Errorf("decode photos: %w", err)
	}
	if ps == nil {
		ps = []Photo{}
	}
	return ps, nil
}

const (
	metaCacheMax = 2000
	metaCacheTTL = time.Hour

	// MaxThumbBytes is the largest thumbnail the proxy will pass on.
	MaxThumbBytes = 5 << 20
	maxThumbConc  = 8
	maxRedirects  = 3
)

// ErrPhotoUnavailable means the photo could not be fetched or is not an
// acceptable JPEG; the cause is deliberately not carried (URLs of signed CDN
// links must not leak into logs).
var ErrPhotoUnavailable = errors.New("mapillary: photo unavailable")

type metaEntry struct {
	meta    ImageMeta
	expires time.Time
}

func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) metaGet(id string) (ImageMeta, bool) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	e, ok := c.meta[id]
	if !ok || !c.clock().Before(e.expires) {
		delete(c.meta, id)
		return ImageMeta{}, false
	}
	return e.meta, true
}

func (c *Client) metaPut(id string, m ImageMeta) {
	c.metaMu.Lock()
	defer c.metaMu.Unlock()
	now := c.clock()
	if c.meta == nil {
		c.meta = map[string]metaEntry{}
	}
	if _, ok := c.meta[id]; !ok && len(c.meta) >= metaCacheMax {
		for k, e := range c.meta {
			if !now.Before(e.expires) {
				delete(c.meta, k)
			}
		}
		for k := range c.meta { // still full: drop one arbitrary entry
			if len(c.meta) < metaCacheMax {
				break
			}
			delete(c.meta, k)
		}
	}
	c.meta[id] = metaEntry{meta: m, expires: now.Add(metaCacheTTL)}
}

func (c *Client) metaDrop(id string) {
	c.metaMu.Lock()
	delete(c.meta, id)
	c.metaMu.Unlock()
}

// Thumb is one downloaded street photo.
type Thumb struct {
	JPEG    []byte
	Creator string
}

// Thumb returns the 2048 px JPEG of image id and its creator. The thumbnail
// location is cached for an hour. The download sends no credentials, refuses
// any host ImageHostOK rejects (also on every redirect hop), keeps at most
// MaxThumbBytes and accepts only a JPEG. Failures other than a rejected
// token or rate limit are ErrPhotoUnavailable.
func (c *Client) Thumb(ctx context.Context, id string) (Thumb, error) {
	meta, ok := c.metaGet(id)
	if !ok {
		var err error
		if meta, err = c.Image(ctx, id); err != nil {
			return Thumb{}, err
		}
		c.metaPut(id, meta)
	}
	data, err := c.fetchThumb(ctx, meta.URL)
	if err != nil {
		c.metaDrop(id) // the signed URL may have expired
		return Thumb{}, err
	}
	return Thumb{JPEG: data, Creator: meta.Creator}, nil
}

func (c *Client) fetchThumb(ctx context.Context, rawURL string) ([]byte, error) {
	if !c.imageHostOK(ctx, rawURL) {
		return nil, ErrBadImageHost
	}
	c.thumbOnce.Do(func() { c.thumbSem = make(chan struct{}, maxThumbConc) })
	select {
	case c.thumbSem <- struct{}{}:
		defer func() { <-c.thumbSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	hc := *c.http()
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return ErrPhotoUnavailable
		}
		if !c.imageHostOK(ctx, req.URL.String()) {
			return ErrBadImageHost
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrPhotoUnavailable
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrBadImageHost) {
			return nil, ErrBadImageHost
		}
		return nil, ErrPhotoUnavailable // the error text carries the signed URL
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrPhotoUnavailable
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "image/jpeg" {
		return nil, ErrPhotoUnavailable
	}
	if resp.ContentLength > MaxThumbBytes {
		return nil, ErrPhotoUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxThumbBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrPhotoUnavailable
	}
	if len(data) > MaxThumbBytes || len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		return nil, ErrPhotoUnavailable
	}
	return data, nil
}
