// Package mapillarytest is an in-process fake of Mapillary's coverage tiles,
// Graph API and thumbnail CDN, for tests and fixtures.
package mapillarytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
)

// Image is one photo the fake serves.
type Image struct {
	ID         int64
	Lat, Lon   float64
	Compass    float64
	CapturedAt int64
	Pano       bool
	Creator    string
}

// Fake serves /tiles/{z}/{x}/{y}, /{id} (Graph) and /thumb/{id}. Set Token
// to require it (access_token query for tiles, OAuth header for Graph).
type Fake struct {
	srv *httptest.Server

	mu          sync.Mutex
	images      []Image
	token       string
	tileStatus  int // non-zero: every tile request answers this status
	thumbStatus int
	thumbType   string
	thumbBody   []byte
	tileGate    chan struct{} // non-nil: tile requests wait until it closes

	tiles, graph, thumbs atomic.Int64
	LastGraphAuth        atomic.Value // last Authorization header of a Graph call
}

// New starts a fake and stops it at test cleanup.
func New(t testing.TB, imgs ...Image) *Fake {
	f := &Fake{images: imgs, thumbType: "image/jpeg", thumbBody: JPEG()}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// JPEG returns a tiny valid JPEG.
func JPEG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		img.Set(x, x, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

func (f *Fake) URL() string      { return f.srv.URL }
func (f *Fake) GraphURL() string { return f.srv.URL }
func (f *Fake) TilesURL() string { return f.srv.URL + "/tiles" }

// Host is the fake's host:port, for an ImageHostOK that admits it.
func (f *Fake) Host() string { return strings.TrimPrefix(f.srv.URL, "http://") }

func (f *Fake) TileRequests() int  { return int(f.tiles.Load()) }
func (f *Fake) GraphRequests() int { return int(f.graph.Load()) }
func (f *Fake) ThumbRequests() int { return int(f.thumbs.Load()) }

func (f *Fake) SetImages(imgs ...Image) { f.set(func() { f.images = imgs }) }
func (f *Fake) SetToken(tok string)     { f.set(func() { f.token = tok }) }

// SetTileStatus makes every tile request answer status (0 restores normal service).
func (f *Fake) SetTileStatus(status int) { f.set(func() { f.tileStatus = status }) }

// SetThumb overrides the thumbnail answer: status (0 = 200), content type and body.
func (f *Fake) SetThumb(status int, contentType string, body []byte) {
	f.set(func() { f.thumbStatus, f.thumbType, f.thumbBody = status, contentType, body })
}

// GateTiles makes tile requests block until the returned func is called (or
// the request is cancelled).
func (f *Fake) GateTiles() (release func()) {
	g := make(chan struct{})
	f.set(func() { f.tileGate = g })
	var once sync.Once
	return func() { once.Do(func() { close(g) }) }
}

func (f *Fake) set(fn func()) { f.mu.Lock(); fn(); f.mu.Unlock() }

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	imgs, token, ts, thS, thT, thB, gate := f.images, f.token, f.tileStatus, f.thumbStatus, f.thumbType, f.thumbBody, f.tileGate
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/tiles/"):
		f.tiles.Add(1)
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if ts != 0 {
			http.Error(w, "status", ts)
			return
		}
		if token != "" && r.URL.Query().Get("access_token") != token {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		f.tile(w, r, imgs)
	case strings.HasPrefix(r.URL.Path, "/thumb/"):
		f.thumbs.Add(1)
		if thS != 0 {
			http.Error(w, "status", thS)
			return
		}
		w.Header().Set("Content-Type", thT)
		_, _ = w.Write(thB)
	default:
		f.graph.Add(1)
		f.LastGraphAuth.Store(r.Header.Get("Authorization"))
		if token != "" && r.Header.Get("Authorization") != "OAuth "+token {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		for _, im := range imgs {
			if im.ID == id {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"thumb_2048_url": f.srv.URL + "/thumb/" + strconv.FormatInt(id, 10),
					"creator":        map[string]string{"username": im.Creator},
					"id":             strconv.FormatInt(id, 10),
				})
				return
			}
		}
		http.NotFound(w, r)
	}
}

func (f *Fake) tile(w http.ResponseWriter, r *http.Request, imgs []Image) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/tiles/"), "/")
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		n[i] = v
	}
	tile := maptile.New(uint32(n[1]), uint32(n[2]), maptile.Zoom(n[0]))
	fc := geojson.NewFeatureCollection()
	for _, im := range imgs {
		if maptile.At(orb.Point{im.Lon, im.Lat}, tile.Z) != tile {
			continue
		}
		ft := geojson.NewFeature(orb.Point{im.Lon, im.Lat})
		ft.Properties["id"] = im.ID
		ft.Properties["compass_angle"] = im.Compass
		ft.Properties["captured_at"] = im.CapturedAt
		ft.Properties["is_pano"] = im.Pano
		fc.Append(ft)
	}
	if len(fc.Features) == 0 {
		http.NotFound(w, r)
		return
	}
	layers := mvt.NewLayers(map[string]*geojson.FeatureCollection{"image": fc})
	layers.ProjectToTile(tile)
	b, err := mvt.Marshal(layers)
	if err != nil {
		http.Error(w, fmt.Sprint(err), 500)
		return
	}
	_, _ = w.Write(b)
}
