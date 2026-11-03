// Package mapillary talks to Mapillary (coverage vector tiles and the Graph
// API) and selects street photos along a route. The access token never
// leaves the server and never appears in an error string.
package mapillary

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"
)

const (
	DefaultGraphURL = "https://graph.mapillary.com"
	DefaultTilesURL = "https://tiles.mapillary.com/maps/vtp/mly1_public/2"
)

var (
	ErrNoToken       = errors.New("mapillary: no access token configured")
	ErrTokenRejected = errors.New("token rejected")
	ErrRateLimited   = errors.New("mapillary: rate limited")
	ErrTileTooLarge  = errors.New("mapillary: coverage tile too large")
	ErrBadImageHost  = errors.New("mapillary: image URL host not allowed")
)

const (
	maxAttempts    = 3
	maxTileBytes   = 32 << 20 // raw (possibly gzipped) tile body
	maxTileDecoded = 64 << 20 // after gunzip
	maxJSONBytes   = 1 << 20
	redactedVal    = "REDACTED"
)

// Client reads Mapillary. The zero value is not usable: set GraphURL, TilesURL
// and Token (HTTP defaults to http.DefaultClient).
type Client struct {
	GraphURL, TilesURL string
	// URLs, if set, supplies the Graph and tile base URLs per call (so a
	// settings change applies at once) and replaces GraphURL and TilesURL.
	URLs  func(ctx context.Context) (graph, tiles string)
	HTTP  *http.Client
	Token func(ctx context.Context) (string, error) // reads sealed settings; "" -> ErrNoToken
	// ImageHostOK reports whether an image URL host may be fetched. Default:
	// the Graph host, *.fbcdn.net or *.mapillary.com.
	ImageHostOK func(host string) bool

	maxRaw, maxDecoded int                 // test overrides for the tile size caps
	sleep              func(time.Duration) // tests only; default waits on a timer or ctx
	now                func() time.Time    // tests only; default time.Now

	metaMu    sync.Mutex
	meta      map[string]metaEntry // image id -> thumbnail location (see cache.go)
	thumbSem  chan struct{}        // bounds concurrent thumbnail downloads
	thumbOnce sync.Once
}

func (c *Client) urls(ctx context.Context) (graph, tiles string) {
	if c.URLs != nil {
		return c.URLs(ctx)
	}
	return c.GraphURL, c.TilesURL
}

// Candidate is one image found in a coverage tile.
type Candidate struct {
	ID         string
	Lat, Lon   float64
	Compass    float64 // degrees clockwise from north
	CapturedAt int64   // Unix milliseconds
	Pano       bool
}

// ImageMeta locates the image bytes.
type ImageMeta struct{ URL, Creator string }

func (c *Client) token(ctx context.Context) (string, error) {
	if c.Token == nil {
		return "", ErrNoToken
	}
	t, err := c.Token(ctx)
	if err != nil {
		return "", err
	}
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// redactURL returns u with any access_token query value replaced.
func redactURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "(unparseable URL)"
	}
	q := p.Query()
	if q.Has("access_token") {
		q.Set("access_token", redactedVal)
		p.RawQuery = q.Encode()
	}
	p.User = nil
	return p.String()
}

// scrub removes the token, raw and in its query and path escaped forms
// (real tokens look like MLY|123|abc, which a URL carries as MLY%7C123%7Cabc),
// from an error text.
func scrub(s, tok string) string {
	if tok == "" {
		return s
	}
	for _, form := range []string{tok, url.QueryEscape(tok), url.PathEscape(tok)} {
		s = strings.ReplaceAll(s, form, redactedVal)
	}
	return s
}

// endpoint joins base and path and sets query, keeping any query already on
// base. Errors are not token bearing.
func endpoint(base, path string, query url.Values) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", errors.New("mapillary: invalid base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	u.Fragment = ""
	q := u.Query()
	for k, v := range query {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// do GETs rawURL with up to maxAttempts tries on 429 and 5xx. headerAuth
// sends the token as "Authorization: OAuth <token>"; the caller puts it in
// the query otherwise. The caller closes the body of a 2xx response.
func (c *Client) do(ctx context.Context, rawURL, tok string, headerAuth bool) (*http.Response, error) {
	var last error
	var delay time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.wait(ctx, delay); err != nil {
				return nil, err
			}
		}
		delay = time.Duration(attempt+1) * time.Second // 1 s, 2 s
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, fmt.Errorf("mapillary: bad request URL %s", redactURL(rawURL))
		}
		if headerAuth {
			req.Header.Set("Authorization", "OAuth "+tok)
		}
		resp, err := c.http().Do(req)
		if err != nil {
			var ue *url.Error
			inner := err
			if errors.As(err, &ue) {
				inner = ue.Err
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("mapillary: GET %s: %s", redactURL(rawURL), scrub(inner.Error(), tok))
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode == http.StatusNotFound:
			return resp, nil
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
			drainClose(resp.Body)
			return nil, fmt.Errorf("%w (%d)", ErrTokenRejected, resp.StatusCode)
		case resp.StatusCode == http.StatusBadRequest && oauthError(resp.Body):
			_ = resp.Body.Close()
			return nil, fmt.Errorf("%w (%d)", ErrTokenRejected, resp.StatusCode)
		case resp.StatusCode == http.StatusTooManyRequests:
			delay = retryAfter(resp, delay)
			drainClose(resp.Body)
			last = ErrRateLimited
		case resp.StatusCode >= 500:
			delay = retryAfter(resp, delay)
			drainClose(resp.Body)
			last = fmt.Errorf("mapillary: HTTP %d from %s", resp.StatusCode, redactURL(rawURL))
		default:
			drainClose(resp.Body)
			return nil, fmt.Errorf("mapillary: HTTP %d from %s", resp.StatusCode, redactURL(rawURL))
		}
	}
	return nil, last
}

// maxRetryAfter caps a server-requested wait.
const maxRetryAfter = 10 * time.Second

// retryAfter returns the Retry-After delay in seconds (capped), else def.
func retryAfter(resp *http.Response, def time.Duration) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || n < 0 {
		return def
	}
	return min(time.Duration(n)*time.Second, maxRetryAfter)
}

// drainClose reads a little of the body so the connection can be reused.
func drainClose(b io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(b, 64<<10))
	_ = b.Close()
}

// oauthError reports whether a 400 body is a Graph API OAuth (token) error,
// and closes nothing.
func oauthError(r io.Reader) bool {
	var e struct {
		Error struct {
			Type string `json:"type"`
			Code int    `json:"code"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&e) != nil {
		return false
	}
	return e.Error.Type == "OAuthException" || e.Error.Code == 190
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		c.sleep(d)
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) rawLimit() int {
	if c.maxRaw > 0 {
		return c.maxRaw
	}
	return maxTileBytes
}

func (c *Client) decodedLimit() int {
	if c.maxDecoded > 0 {
		return c.maxDecoded
	}
	return maxTileDecoded
}

// Coverage lists the images in the z/x/y coverage tile (zoom 14 carries
// individual images). A tile without coverage is nil, nil.
func (c *Client) Coverage(ctx context.Context, z, x, y int) ([]Candidate, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return nil, err
	}
	_, tiles := c.urls(ctx)
	u, err := endpoint(tiles, fmt.Sprintf("/%d/%d/%d", z, x, y), url.Values{"access_token": {tok}})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, u, tok, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.rawLimit())+1))
	if err != nil {
		return nil, fmt.Errorf("mapillary: read tile: %s", scrub(err.Error(), tok))
	}
	if len(body) > c.rawLimit() {
		return nil, ErrTileTooLarge
	}
	var layers mvt.Layers
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("mapillary: decode tile: %w", err)
		}
		body, err = io.ReadAll(io.LimitReader(zr, int64(c.decodedLimit())+1))
		if err == nil && len(body) > c.decodedLimit() {
			return nil, ErrTileTooLarge
		}
		if err != nil {
			return nil, errors.New("mapillary: decode tile: bad gzip")
		}
	}
	layers, err = mvt.Unmarshal(body)
	if err != nil {
		return nil, fmt.Errorf("mapillary: decode tile: %w", err)
	}
	layers.ProjectToWGS84(maptile.New(uint32(x), uint32(y), maptile.Zoom(z)))
	var out []Candidate
	for _, l := range layers {
		if l.Name != "image" {
			continue
		}
		for _, f := range l.Features {
			var pts []orb.Point
			switch g := f.Geometry.(type) {
			case orb.Point:
				pts = []orb.Point{g}
			case orb.MultiPoint:
				pts = g
			}
			id := propString(f.Properties["id"])
			if id == "" && f.ID != nil {
				id = propString(f.ID)
			}
			if id == "" {
				continue
			}
			for _, p := range pts {
				out = append(out, Candidate{
					ID: id, Lat: p[1], Lon: p[0],
					Compass:    propFloat(f.Properties["compass_angle"]),
					CapturedAt: int64(propFloat(f.Properties["captured_at"])),
					Pano:       propBool(f.Properties["is_pano"]),
				})
			}
		}
	}
	return out, nil
}

func propFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

func propBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	default:
		return propFloat(v) != 0
	}
}

// propString renders a tile property as an id. orb decodes every MVT integer
// as float64, so ids above 2^53 would lose precision; Mapillary ids are
// about 1e15 (below 2^53), so this is exact in practice.
func propString(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case float64:
		if n == math.Trunc(n) {
			return strconv.FormatInt(int64(n), 10)
		}
	case float32, int, int64, uint64:
		return strconv.FormatInt(int64(propFloat(n)), 10)
	}
	return ""
}

// Image looks up the 2048 px thumbnail URL and creator of an image.
func (c *Client) Image(ctx context.Context, id string) (ImageMeta, error) {
	tok, err := c.token(ctx)
	if err != nil {
		return ImageMeta{}, err
	}
	if id == "" || strings.Trim(id, "0123456789") != "" {
		return ImageMeta{}, errors.New("mapillary: bad image id")
	}
	graph, _ := c.urls(ctx)
	u, err := endpoint(graph, "/"+id, url.Values{"fields": {"thumb_2048_url,creator"}})
	if err != nil {
		return ImageMeta{}, err
	}
	resp, err := c.do(ctx, u, tok, true)
	if err != nil {
		return ImageMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ImageMeta{}, fmt.Errorf("mapillary: image %s not found", id)
	}
	var v struct {
		URL     string `json:"thumb_2048_url"`
		Creator struct {
			Username string `json:"username"`
		} `json:"creator"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBytes)).Decode(&v); err != nil {
		return ImageMeta{}, errors.New("mapillary: unreadable image reply")
	}
	if v.URL == "" {
		return ImageMeta{}, fmt.Errorf("mapillary: image %s has no thumbnail", id)
	}
	if !c.imageHostOK(ctx, v.URL) {
		return ImageMeta{}, ErrBadImageHost
	}
	return ImageMeta{URL: v.URL, Creator: v.Creator.Username}, nil
}

func (c *Client) imageHostOK(ctx context.Context, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return false
	}
	if c.ImageHostOK != nil {
		return c.ImageHostOK(u.Host)
	}
	graph, _ := c.urls(ctx)
	if g, err := url.Parse(graph); err == nil && g.Host != "" && strings.EqualFold(u.Host, g.Host) {
		return true
	}
	h := strings.ToLower(u.Hostname())
	return strings.HasSuffix(h, ".fbcdn.net") || strings.HasSuffix(h, ".mapillary.com")
}

// Ping fetches one z14 coverage tile (central Berlin) with the token; nil means
// the token is accepted. A tile with no coverage (404) still counts. The tiles
// host answers in well under a second, unlike the Graph API image search.
func (c *Client) Ping(ctx context.Context) error {
	tok, err := c.token(ctx)
	if err != nil {
		return err
	}
	_, tiles := c.urls(ctx)
	u, err := endpoint(tiles, fmt.Sprintf("/14/%d/%d", pingTileX, pingTileY), url.Values{"access_token": {tok}})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, u, tok, false)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// z14 tile covering central Berlin (13.405, 52.52), used by Ping.
const (
	pingTileX = 8802
	pingTileY = 5373
)
