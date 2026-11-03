package route

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// LonLat is [lon, lat], matching GeoJSON and the draw API.
type LonLat [2]float64

const (
	maxBRouterBody  = 32 << 20 // successful GeoJSON
	maxBRouterError = 64 << 10 // error text
	maxMessageLen   = 300
	maxWaypoints    = 100
	defaultTimeout  = 90 * time.Second
)

// BRouter is a client for a BRouter server (BaseURL without a trailing path).
type BRouter struct {
	BaseURL string
	Client  *http.Client // nil = a client with a bounded timeout
}

// MissingTilesError carries the segment tiles a BRouter server lacks, as a
// structured value so the API can return them without parsing message text.
type MissingTilesError struct{ Tiles []string }

func (e *MissingTilesError) Error() string {
	return "brouter: missing tiles " + strings.Join(e.Tiles, ", ")
}

// BRouterError is a non-200 BRouter reply.
type BRouterError struct {
	Status       int
	Message      string
	MissingTiles []string // from "datafile E5_N45.rd5 not found" style replies
	// LookupMismatch is set when BRouter's lookups.dat and the rd5 segments are
	// different versions ("lookup version mismatch (old rd5?) lookups.dat=10 ...rd5=11").
	LookupMismatch bool
}

func (e *BRouterError) Error() string { return "brouter: " + e.Message }

// Unwrap exposes MissingTiles as a *MissingTilesError (nil when there are none).
func (e *BRouterError) Unwrap() error {
	if len(e.MissingTiles) == 0 {
		return nil
	}
	return &MissingTilesError{Tiles: e.MissingTiles}
}

var profileRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var rd5RE = regexp.MustCompile(`([EW]\d+_[NS]\d+)\.rd5`)

// Route asks BRouter for a route through the waypoints with the profile.
func (b BRouter) Route(ctx context.Context, wps []LonLat, profile string) ([]Point, error) {
	if len(wps) < 2 || len(wps) > maxWaypoints {
		return nil, fmt.Errorf("brouter: need 2..%d waypoints, got %d", maxWaypoints, len(wps))
	}
	if !profileRE.MatchString(profile) {
		return nil, errors.New("brouter: invalid profile name")
	}
	parts := make([]string, len(wps))
	for i, w := range wps {
		if !(w[1] >= -90 && w[1] <= 90 && w[0] >= -180 && w[0] <= 180) {
			return nil, fmt.Errorf("brouter: waypoint %d out of range", i)
		}
		parts[i] = fmt.Sprintf("%.6f,%.6f", w[0], w[1])
	}
	q := url.Values{}
	q.Set("lonlats", strings.Join(parts, "|"))
	q.Set("profile", profile)
	q.Set("alternativeidx", "0")
	q.Set("format", "geojson")
	u := strings.TrimRight(b.BaseURL, "/") + "/brouter?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("brouter: bad base URL")
	}
	resp, err := clientOrDefault(b.Client).Do(req)
	if err != nil {
		return nil, transportErr("brouter", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBRouterError))
		return nil, newBRouterError(resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBRouterBody+1))
	if err != nil {
		return nil, transportErr("brouter", err)
	}
	if len(body) > maxBRouterBody {
		return nil, errors.New("brouter: response too large")
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Coordinates [][]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(body, &fc); err != nil {
		return nil, errors.New("brouter: invalid GeoJSON reply")
	}
	if len(fc.Features) == 0 || len(fc.Features[0].Geometry.Coordinates) < 2 {
		return nil, errors.New("brouter: empty route")
	}
	coords := fc.Features[0].Geometry.Coordinates
	pts := make([]Point, 0, len(coords))
	for _, c := range coords {
		if len(c) < 2 {
			return nil, errors.New("brouter: invalid coordinate")
		}
		ele := math.NaN()
		if len(c) > 2 {
			ele = c[2]
		}
		p, ok := newPoint(c[1], c[0], ele)
		if !ok {
			return nil, errors.New("brouter: invalid coordinate")
		}
		pts = append(pts, p)
	}
	return pts, nil
}

func newBRouterError(status int, body string) *BRouterError {
	msg := strings.TrimSpace(strings.ToValidUTF8(body, ""))
	var tiles []string
	seen := map[string]bool{}
	mismatch := strings.Contains(msg, "lookup version mismatch")
	// Only a "not found" reply means the tile is absent; other replies that name an
	// rd5 file (a version mismatch) must not send the user to download it again.
	var matches [][]string
	if !mismatch && strings.Contains(msg, "not found") {
		matches = rd5RE.FindAllStringSubmatch(msg, -1)
	}
	for _, m := range matches {
		if _, _, err := ParseTile(m[1]); err == nil && !seen[m[1]] {
			seen[m[1]] = true
			tiles = append(tiles, m[1])
		}
	}
	sort.Strings(tiles)
	if utf8.RuneCountInString(msg) > maxMessageLen {
		msg = string([]rune(msg)[:maxMessageLen])
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &BRouterError{Status: status, Message: msg, MissingTiles: tiles, LookupMismatch: mismatch}
}

func clientOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultTimeout}
}

// transportErr wraps a transport failure without the request URL (which may
// carry credentials) while keeping the cause for errors.Is.
func transportErr(who string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("%s: request failed: %w", who, err)
}
