package strava_test

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/secretbox"
	"github.com/metril/wattson/internal/settings"
	"github.com/metril/wattson/internal/store"
	"github.com/metril/wattson/internal/store/storetest"
)

// processing answers Strava's upload requests in memory (no sockets, so a
// synctest bubble can advance its clock): every upload stays "still
// processing" and status polls are counted.
type processing struct{ polls atomic.Int64 }

func (p *processing) RoundTrip(r *http.Request) (*http.Response, error) {
	body := `{"id":1,"external_id":"x","status":"Your activity is still being processed.","error":null,"activity_id":null}`
	code := http.StatusOK
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/uploads"):
		code = http.StatusCreated
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/uploads/1"):
		p.polls.Add(1)
	default:
		code, body = http.StatusNotFound, `{"message":"no"}`
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// bubbleEnv builds a service (default pacing: 2 s polls, 2 min deadline) over
// the in-memory Strava with a connected rider and a finished 30-sample ride.
func bubbleEnv(t *testing.T) (*strava.Service, *store.Store, *processing, store.Ride) {
	t.Helper()
	ctx := context.Background()
	st := storetest.New(t)
	box, err := secretbox.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, store.User{Username: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	rd, err := st.CreateRider(ctx, store.DefaultRider(u.ID, "Ada"))
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(rd.ID, 10)
	if err := st.PutStravaLink(ctx, store.StravaLink{RiderID: rd.ID, AthleteID: 42, Scope: strava.Scope,
		AccessEnc:  box.SealFor("a1", "strava.rider."+id+".access_token"),
		RefreshEnc: box.SealFor("r1", "strava.rider."+id+".refresh_token"), ExpiresAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	p := &processing{}
	svc := strava.NewService(strava.ServiceDeps{Store: st, Box: box, Settings: settings.NewService(st, box),
		Client: strava.NewClient(strava.Prod, &http.Client{Transport: p})})
	t.Cleanup(svc.Close)

	r, err := st.CreateRide(ctx, store.Ride{RiderID: rd.ID, Mode: "free", StartedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	ss := make([]store.Sample, 30)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1), DistanceM: float64(10 * i),
			SpeedKmh: 30, PowerW: 150, CadenceRpm: 80, Level: 5, Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
	}
	if err := st.AppendSamples(ctx, r.ID, ss); err != nil {
		t.Fatal(err)
	}
	if r, err = st.FinishRide(ctx, r.ID, ss[29].At, history.Summarize(ss), len(ss)); err != nil {
		t.Fatal(err)
	}
	return svc, st, p, r
}

func TestPollCadenceAndTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		svc, st, p, r := bubbleEnv(t)
		ctx := context.Background()
		if _, started, err := svc.Upload(ctx, r.ID); err != nil || !started {
			t.Fatalf("upload: started %v, %v", started, err)
		}
		time.Sleep(11 * time.Second)
		synctest.Wait()
		if n := p.polls.Load(); n != 5 {
			t.Errorf("polls after 11 s = %d, want 5 (one every 2 s)", n)
		}
		if got, _ := st.Ride(ctx, r.ID); got.Strava.Status != "uploading" || got.Strava.UploadID != 1 {
			t.Errorf("status = %+v", got.Strava)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		got, _ := st.Ride(ctx, r.ID)
		if got.Strava.Status != "error" || got.Strava.Error != "Strava is still processing; check again later" || got.Strava.UploadID != 1 {
			t.Errorf("after the deadline: %+v", got.Strava)
		}
		if n := p.polls.Load(); n < 59 || n > 60 {
			t.Errorf("polls within 2 min = %d, want 59 or 60", n)
		}
	})
}

func TestDeletedRideStopsPolling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		svc, st, p, r := bubbleEnv(t)
		ctx := context.Background()
		if _, _, err := svc.Upload(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Second)
		if err := st.DeleteRide(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		n := p.polls.Load()
		time.Sleep(time.Minute)
		synctest.Wait()
		if got := p.polls.Load(); got != n {
			t.Errorf("polling went on after the ride was deleted: %d -> %d", n, got)
		}
	})
}
