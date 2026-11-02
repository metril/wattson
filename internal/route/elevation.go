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
	"time"
)

// ElevationFiller supplies elevations for points that lack trustworthy ones.
type ElevationFiller interface {
	Fill(ctx context.Context, pts []Point) error
}

const (
	otdBatch      = 100
	maxOTDBody    = 1 << 20
	maxOTDSamples = 5000
	otdTimeout    = 30 * time.Second
)

// OpenTopoData fills elevations from an OpenTopoData API (BaseURL includes the
// dataset, e.g. https://api.opentopodata.org/v1/srtm90m).
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
// up, then sets every point's Ele by linear interpolation over distance. pts is
// left untouched on error, and when no sample has an elevation.
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
		if from > 0 && o.Interval > 0 {
			t := time.NewTimer(o.Interval)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
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

	// Known samples only; null elevations are skipped by the interpolation.
	var kd, ke []float64
	for k, i := range idx {
		if !math.IsNaN(ele[k]) {
			kd, ke = append(kd, dist[i]), append(ke, ele[k])
		}
	}
	if len(kd) == 0 {
		return nil
	}
	j := 0
	for i := range pts {
		for j+1 < len(kd) && kd[j+1] <= dist[i] {
			j++
		}
		switch {
		case dist[i] <= kd[0]:
			pts[i].Ele = ke[0]
		case j+1 >= len(kd):
			pts[i].Ele = ke[len(ke)-1]
		default:
			t := (dist[i] - kd[j]) / (kd[j+1] - kd[j])
			pts[i].Ele = ke[j] + (ke[j+1]-ke[j])*t
		}
	}
	return nil
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
			return err
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
