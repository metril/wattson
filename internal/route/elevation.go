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
	"strings"
	"sync"
	"time"
)

// ErrElevationUnavailable wraps a failure of the elevation service (the cause
// stays reachable through errors.Is/As).
var ErrElevationUnavailable = errors.New("route: elevation service unavailable")

// ElevationFiller supplies elevations for points that lack trustworthy ones.
type ElevationFiller interface {
	Fill(ctx context.Context, pts []Point) error
}

const (
	otdBatch      = 100
	maxOTDBody    = 1 << 20
	maxOTDSamples = 6000
	otdTimeout    = 30 * time.Second
)

// OpenTopoData fills elevations from an OpenTopoData API (BaseURL includes the
// dataset, e.g. https://api.opentopodata.org/v1/mapzen).
type OpenTopoData struct {
	BaseURL  string
	Client   *http.Client  // nil = bounded timeout
	Interval time.Duration // pause between requests (public API: 1s); 0 = none
	SampleM  float64       // sampling distance; <= 0 = 100
}

// NewOpenTopoData returns a client with the public-API defaults.
func NewOpenTopoData(baseURL string) OpenTopoData {
	return OpenTopoData{BaseURL: baseURL, Interval: time.Second, SampleM: 100}
}

// Fill samples the track every SampleM (plus the last point), looks the samples
// up, then sets every point's Ele by linear interpolation between the two
// adjacent samples; a point whose bracketing samples include a null stays NaN,
// so PrepareElevation's missing-data check still fires. pts is left untouched
// on error. Requests are rate limited process-wide per BaseURL (Interval).
func (o OpenTopoData) Fill(ctx context.Context, pts []Point) error {
	if len(pts) == 0 {
		return nil
	}
	step := o.SampleM
	if !(step > 0) {
		step = 100
	}
	dist := make([]float64, len(pts))
	for i := 1; i < len(pts); i++ {
		dist[i] = dist[i-1] + haversine(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
	}
	idx := []int{0}
	next := step
	for i := 1; i < len(pts); i++ {
		if dist[i] >= next {
			idx = append(idx, i)
			next = dist[i] + step
		}
	}
	if idx[len(idx)-1] != len(pts)-1 {
		idx = append(idx, len(pts)-1)
	}
	if len(idx) > maxOTDSamples {
		return errors.New("opentopodata: track too long")
	}

	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: otdTimeout}
	}
	ele := make([]float64, 0, len(idx))
	for from := 0; from < len(idx); from += otdBatch {
		if err := limiterFor(o.BaseURL).wait(ctx, o.Interval); err != nil {
			return err
		}
		to := min(from+otdBatch, len(idx))
		locs := make([]string, 0, to-from)
		for _, i := range idx[from:to] {
			locs = append(locs, fmt.Sprintf("%.6f,%.6f", pts[i].Lat, pts[i].Lon))
		}
		got, err := o.fetch(ctx, client, locs)
		if err != nil {
			return err
		}
		ele = append(ele, got...)
	}

	// Interpolate only between adjacent samples that both have an elevation.
	j := 0
	for i := range pts {
		for j+1 < len(idx)-1 && dist[idx[j+1]] <= dist[i] {
			j++
		}
		lo, hi := idx[j], idx[min(j+1, len(idx)-1)]
		el, eh := ele[j], ele[min(j+1, len(idx)-1)]
		switch {
		case i == lo:
			pts[i].Ele = el
		case i == hi:
			pts[i].Ele = eh
		case math.IsNaN(el) || math.IsNaN(eh):
			pts[i].Ele = math.NaN()
		default:
			t := (dist[i] - dist[lo]) / (dist[hi] - dist[lo])
			pts[i].Ele = el + (eh-el)*t
		}
	}
	return nil
}

// limiter spaces request starts at least one interval apart.
type limiter struct {
	mu   sync.Mutex
	next time.Time
}

var (
	limitersMu sync.Mutex
	limiters   = map[string]*limiter{}
)

func limiterFor(baseURL string) *limiter {
	limitersMu.Lock()
	defer limitersMu.Unlock()
	l := limiters[baseURL]
	if l == nil {
		l = &limiter{}
		limiters[baseURL] = l
	}
	return l
}

func (l *limiter) wait(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return ctx.Err()
	}
	l.mu.Lock()
	slot := time.Now()
	if l.next.After(slot) {
		slot = l.next
	}
	l.next = slot.Add(interval)
	l.mu.Unlock()
	d := time.Until(slot)
	if d <= 0 {
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

func (o OpenTopoData) fetch(ctx context.Context, c *http.Client, locs []string) ([]float64, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("opentopodata: bad base URL")
	}
	q := u.Query()
	q.Set("locations", strings.Join(locs, "|"))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("opentopodata: bad base URL")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, transportErr("opentopodata", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOTDBody+1))
	if err != nil {
		return nil, transportErr("opentopodata", err)
	}
	if len(body) > maxOTDBody {
		return nil, errors.New("opentopodata: response too large")
	}
	var r struct {
		Status  string `json:"status"`
		Results []struct {
			Elevation *float64 `json:"elevation"`
		} `json:"results"`
	}
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal(body, &r)
		return nil, fmt.Errorf("opentopodata: HTTP %d %s", resp.StatusCode, safeStatus(r.Status))
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, errors.New("opentopodata: invalid reply")
	}
	if r.Status != "OK" {
		return nil, fmt.Errorf("opentopodata: status %s", safeStatus(r.Status))
	}
	if len(r.Results) != len(locs) {
		return nil, errors.New("opentopodata: result count mismatch")
	}
	out := make([]float64, len(locs))
	for i, e := range r.Results {
		if e.Elevation == nil || !(*e.Elevation >= -1000 && *e.Elevation <= 20000) {
			out[i] = math.NaN()
		} else {
			out[i] = *e.Elevation
		}
	}
	return out, nil
}

func safeStatus(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return -1
		}
		return r
	}, s)
}

// PrepareElevation fills elevations through f when the track is above 60
// degrees latitude (SRTM coverage ends there) or more than 10% of points lack
// one. f == nil disables filling. It returns ErrNoElevation when more than 10%
// still lack elevation afterwards.
func PrepareElevation(ctx context.Context, pts []Point, f ElevationFiller) error {
	if len(pts) == 0 {
		return nil
	}
	maxLat := 0.0
	for _, p := range pts {
		maxLat = math.Max(maxLat, math.Abs(p.Lat))
	}
	if maxLat <= 60 && nanFraction(pts) <= 0.1 {
		return nil
	}
	if f != nil {
		if err := f.Fill(ctx, pts); err != nil {
			return fmt.Errorf("%w: %w", ErrElevationUnavailable, err)
		}
	}
	if nanFraction(pts) > 0.1 {
		return ErrNoElevation
	}
	return nil
}

func nanFraction(pts []Point) float64 {
	n := 0
	for _, p := range pts {
		if math.IsNaN(p.Ele) || math.IsInf(p.Ele, 0) {
			n++
		}
	}
	return float64(n) / float64(len(pts))
}
