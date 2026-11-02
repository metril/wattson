package mapillary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metril/wattson/internal/mapillary/mapillarytest"
)

func thumbClient(f *mapillarytest.Fake) *Client {
	return &Client{GraphURL: f.GraphURL(), TilesURL: f.TilesURL(), HTTP: &http.Client{},
		Token:       func(context.Context) (string, error) { return testToken, nil },
		ImageHostOK: func(h string) bool { return h == f.Host() }, sleep: func(time.Duration) {}}
}

func TestThumbOKAndMetaCached(t *testing.T) {
	t.Parallel()
	f := mapillarytest.New(t, mapillarytest.Image{ID: 7, Creator: "ana"})
	f.SetToken(testToken)
	c := thumbClient(f)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		th, err := c.Thumb(context.Background(), "7")
		if err != nil || th.Creator != "ana" || !bytes.Equal(th.JPEG, mapillarytest.JPEG()) {
			t.Fatalf("thumb = %+v, %v", th, err)
		}
	}
	if f.GraphRequests() != 1 || f.ThumbRequests() != 2 {
		t.Errorf("graph=%d thumbs=%d, want 1 and 2", f.GraphRequests(), f.ThumbRequests())
	}
	now = now.Add(metaCacheTTL + time.Second)
	if _, err := c.Thumb(context.Background(), "7"); err != nil || f.GraphRequests() != 2 {
		t.Errorf("after expiry graph=%d err=%v, want 2", f.GraphRequests(), err)
	}
}

func TestThumbMetaCacheBounded(t *testing.T) {
	t.Parallel()
	c := &Client{}
	for i := 0; i < metaCacheMax+50; i++ {
		c.metaPut(fmt.Sprint(i), ImageMeta{URL: "u"})
	}
	if len(c.meta) > metaCacheMax {
		t.Errorf("cache holds %d entries, max %d", len(c.meta), metaCacheMax)
	}
}

func TestThumbRefusals(t *testing.T) {
	t.Parallel()
	jpg := mapillarytest.JPEG()
	big := append(append([]byte{0xFF, 0xD8, 0xFF}, make([]byte, MaxThumbBytes)...), 0)
	for _, tc := range []struct {
		name   string
		status int
		ctype  string
		body   []byte
	}{
		{"500", 500, "image/jpeg", jpg},
		{"html", 200, "text/html", []byte("<script>")},
		{"png type", 200, "image/png", jpg},
		{"no type", 200, "", jpg},
		{"jpeg type, not a jpeg", 200, "image/jpeg", []byte("<html>")},
		{"too big", 200, "image/jpeg", big},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := mapillarytest.New(t, mapillarytest.Image{ID: 7})
			f.SetThumb(tc.status, tc.ctype, tc.body)
			_, err := thumbClient(f).Thumb(context.Background(), "7")
			if !errors.Is(err, ErrPhotoUnavailable) {
				t.Errorf("err = %v, want ErrPhotoUnavailable", err)
			}
		})
	}
}

func TestThumbRedirectsAreHostChecked(t *testing.T) {
	t.Parallel()
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		_, _ = w.Write(mapillarytest.JPEG())
	}))
	t.Cleanup(evil.Close)
	var good *httptest.Server
	good = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/thumb/evil"):
			http.Redirect(w, r, evil.URL+"/x.jpg", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/thumb/loop"):
			http.Redirect(w, r, good.URL+"/thumb/loop", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/thumb/hop"):
			http.Redirect(w, r, good.URL+"/thumb/ok", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/thumb/ok"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(mapillarytest.JPEG())
		default:
			id := strings.TrimPrefix(r.URL.Path, "/")
			fmt.Fprintf(w, `{"thumb_2048_url":%q,"creator":{"username":"u"}}`, good.URL+"/thumb/"+id)
		}
	}))
	t.Cleanup(good.Close)
	goodHost := strings.TrimPrefix(good.URL, "http://")
	c := &Client{GraphURL: good.URL, HTTP: &http.Client{}, ImageHostOK: func(h string) bool { return h == goodHost },
		Token: func(context.Context) (string, error) { return testToken, nil }, sleep: func(time.Duration) {}}
	// Graph ids must be numeric, so reach fetchThumb directly.
	if _, err := c.fetchThumb(context.Background(), good.URL+"/thumb/hop"); err != nil {
		t.Errorf("allowed redirect: %v", err)
	}
	if _, err := c.fetchThumb(context.Background(), good.URL+"/thumb/evil"); !errors.Is(err, ErrBadImageHost) {
		t.Errorf("redirect to a foreign host: %v, want ErrBadImageHost", err)
	}
	if evilHits.Load() != 0 {
		t.Errorf("foreign host was contacted %d times", evilHits.Load())
	}
	if _, err := c.fetchThumb(context.Background(), good.URL+"/thumb/loop"); !errors.Is(err, ErrPhotoUnavailable) {
		t.Errorf("redirect loop: %v, want ErrPhotoUnavailable", err)
	}
	if _, err := c.fetchThumb(context.Background(), evil.URL+"/x.jpg"); !errors.Is(err, ErrBadImageHost) || evilHits.Load() != 0 {
		t.Errorf("initial foreign host: %v hits=%d", err, evilHits.Load())
	}
}
