// Package stravatest is an httptest fake of the Strava OAuth and upload
// endpoints for tests of the strava client and of everything above it.
package stravatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/muktihari/fit/decoder"

	"github.com/metril/wattson/internal/history"
	"github.com/metril/wattson/internal/integrations/fitexport"
	"github.com/metril/wattson/internal/integrations/strava"
	"github.com/metril/wattson/internal/store"
)

// ActivityID is the activity every successful upload ends up as.
const ActivityID = 777

// Received is one accepted upload request.
type Received struct {
	Name, Description, ExternalID, DataType, Trainer, Bearer string
	FIT                                                      []byte
}

type upload struct {
	id       int64
	external string
	polls    int    // processing answers still to give
	errMsg   string // final error instead of an activity
}

// Fake is a fake Strava. Create it with New.
type Fake struct {
	srv *httptest.Server

	mu          sync.Mutex
	appID       string
	appSecret   string
	scope       string
	deny        bool
	unauth      bool
	athlete     [3]any // id, first, last of the athlete tokens carry
	deauthCode  int    // status /oauth/deauthorize answers; 0 means 200
	polls       int
	failStatus  int // next uploads-endpoint request answers this status
	failMsg     string
	failFinal   string // next accepted upload ends with this processing error
	dupOf       int64
	codes       int
	validRefr   string
	received    []Received
	uploads     map[int64]*upload
	deauthorize []string
}

// New starts a fake with app "123"/"shh"; t closes it on cleanup.
func New(t testing.TB) *Fake {
	f := &Fake{athlete: [3]any{42, "Ada", "Lovelace"}, appID: "123", appSecret: "shh", scope: strava.Scope, polls: 1, uploads: map[int64]*upload{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /oauth/authorize", f.authorize)
	mux.HandleFunc("POST /oauth/token", f.token)
	mux.HandleFunc("POST /oauth/deauthorize", f.deauth)
	mux.HandleFunc("POST /api/v3/uploads", f.postUpload)
	mux.HandleFunc("GET /api/v3/uploads/{id}", f.getUpload)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// Endpoints are the fake's URLs.
func (f *Fake) Endpoints() strava.Endpoints {
	return strava.Endpoints{AuthURL: f.srv.URL + "/oauth/authorize", TokenURL: f.srv.URL + "/oauth/token",
		DeauthURL: f.srv.URL + "/oauth/deauthorize", APIBase: f.srv.URL + "/api/v3"}
}

// GrantScope sets the scope the user "grants" at /oauth/authorize.
func (f *Fake) GrantScope(s string) { f.locked(func() { f.scope = s }) }

// Deny makes /oauth/authorize answer access_denied.
func (f *Fake) Deny() { f.locked(func() { f.deny = true }) }

// ProcessingPolls sets how many status polls answer "still processing"
// before the upload finishes (default 1). It applies to later uploads.
func (f *Fake) ProcessingPolls(n int) { f.locked(func() { f.polls = n }) }

// FailNextUpload fails the next upload. A status of 400 or more answers
// that HTTP status (and message errMsg) to the next request on the uploads
// endpoints, POST or status poll, whichever comes first. A lower status
// (use 0) accepts the next upload and ends its processing with the error
// errMsg.
func (f *Fake) FailNextUpload(status int, errMsg string) {
	f.locked(func() {
		if status >= 400 {
			f.failStatus, f.failMsg = status, errMsg
		} else {
			f.failFinal = errMsg
		}
	})
}

// DuplicateOf makes the next accepted upload end with Strava's duplicate
// error naming activityID.
func (f *Fake) DuplicateOf(activityID int64) { f.locked(func() { f.dupOf = activityID }) }

// Unauthorized makes every uploads request answer 401 while on.
func (f *Fake) Unauthorized(on bool) { f.locked(func() { f.unauth = on }) }

// FailDeauthorize makes /oauth/deauthorize answer status (still recording the token).
func (f *Fake) FailDeauthorize(status int) { f.locked(func() { f.deauthCode = status }) }

// SetAthlete changes the athlete later token responses carry (default 42, Ada Lovelace).
func (f *Fake) SetAthlete(id int64, first, last string) {
	f.locked(func() { f.athlete = [3]any{id, first, last} })
}

// SetApp changes the accepted client credentials.
func (f *Fake) SetApp(id, secret string) { f.locked(func() { f.appID, f.appSecret = id, secret }) }

// Uploads returns the accepted upload requests in order.
func (f *Fake) Uploads() []Received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Received(nil), f.received...)
}

// Deauthorized returns the access tokens revoked so far.
func (f *Fake) Deauthorized() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deauthorize...)
}

func (f *Fake) locked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *Fake) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if q.Get("client_id") != f.appID || err != nil || redirect.Host == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	v := redirect.Query()
	v.Set("state", q.Get("state"))
	if f.deny {
		v.Set("error", "access_denied")
	} else {
		f.codes++
		v.Set("code", "c"+strconv.Itoa(f.codes))
		v.Set("scope", f.scope)
	}
	redirect.RawQuery = v.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *Fake) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.PostForm.Get("client_id") != f.appID || r.PostForm.Get("client_secret") != f.appSecret {
		jsonOut(w, http.StatusUnauthorized, map[string]any{"message": "Authorization Error"})
		return
	}
	n := 1
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if !strings.HasPrefix(r.PostForm.Get("code"), "c") {
			jsonOut(w, http.StatusBadRequest, map[string]any{"message": "Bad Request"})
			return
		}
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		num, err := strconv.Atoi(strings.TrimPrefix(rt, "r"))
		if rt == "" || rt != f.validRefr || err != nil {
			jsonOut(w, http.StatusBadRequest, map[string]any{"message": "Bad Request"})
			return
		}
		n = num + 1
	default:
		jsonOut(w, http.StatusBadRequest, map[string]any{"message": "Bad Request"})
		return
	}
	f.validRefr = "r" + strconv.Itoa(n)
	jsonOut(w, http.StatusOK, map[string]any{
		"token_type": "Bearer", "access_token": "a" + strconv.Itoa(n), "refresh_token": f.validRefr,
		"expires_at": time.Now().Add(6 * time.Hour).Unix(), "expires_in": 6 * 3600, "scope": f.scope,
		"athlete": map[string]any{"id": f.athlete[0], "firstname": f.athlete[1], "lastname": f.athlete[2]},
	})
}

func (f *Fake) deauth(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	tok := r.PostForm.Get("access_token")
	if tok == "" {
		tok = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	code := http.StatusOK
	f.locked(func() {
		f.deauthorize = append(f.deauthorize, tok)
		if f.deauthCode != 0 {
			code = f.deauthCode
		}
	})
	jsonOut(w, code, map[string]any{"access_token": tok})
}

// gate applies the 401 and one-shot failure switches; true means the
// request was answered.
func (f *Fake) gate(w http.ResponseWriter, r *http.Request) bool {
	if f.unauth || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer a") {
		jsonOut(w, http.StatusUnauthorized, map[string]any{"message": "Authorization Error"})
		return true
	}
	if f.failStatus != 0 {
		st, msg := f.failStatus, f.failMsg
		f.failStatus, f.failMsg = 0, ""
		jsonOut(w, st, map[string]any{"message": msg})
		return true
	}
	return false
}

func (u *upload) json(final bool) map[string]any {
	m := map[string]any{"id": u.id, "id_str": strconv.FormatInt(u.id, 10), "external_id": u.external,
		"status": "Your activity is still being processed.", "error": nil, "activity_id": nil}
	switch {
	case !final:
	case u.errMsg != "":
		m["status"], m["error"] = "There was an error processing your activity.", u.errMsg
	default:
		m["status"], m["activity_id"] = "Your activity is ready.", ActivityID
	}
	return m
}

func (f *Fake) postUpload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gate(w, r) {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"message": "bad multipart"})
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"message": "file is required"})
		return
	}
	defer file.Close()
	fit, err := io.ReadAll(file)
	if err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"message": "bad file"})
		return
	}
	if _, err := decoder.New(bytes.NewReader(fit)).CheckIntegrity(); err != nil {
		jsonOut(w, http.StatusBadRequest, map[string]any{"message": "The file is not a valid FIT file"})
		return
	}
	f.received = append(f.received, Received{Name: r.FormValue("name"), Description: r.FormValue("description"),
		ExternalID: r.FormValue("external_id"), DataType: r.FormValue("data_type"), Trainer: r.FormValue("trainer"),
		Bearer: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), FIT: fit})
	u := &upload{id: int64(len(f.received)), external: r.FormValue("external_id"), polls: f.polls}
	switch {
	case f.dupOf != 0:
		u.errMsg, f.dupOf = fmt.Sprintf("%s duplicate of activity %d", hdr.Filename, f.dupOf), 0
	case f.failFinal != "":
		u.errMsg, f.failFinal = f.failFinal, ""
	}
	f.uploads[u.id] = u
	jsonOut(w, http.StatusCreated, u.json(false))
}

func (f *Fake) getUpload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gate(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	u := f.uploads[id]
	if u == nil {
		jsonOut(w, http.StatusNotFound, map[string]any{"message": "Record Not Found"})
		return
	}
	if u.polls > 0 {
		u.polls--
		jsonOut(w, http.StatusOK, u.json(false))
		return
	}
	jsonOut(w, http.StatusOK, u.json(true))
}

// SampleFIT returns a small valid FIT activity file for upload tests.
func SampleFIT(t testing.TB) []byte {
	t.Helper()
	t0 := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	ss := make([]store.Sample, 30)
	for i := range ss {
		ss[i] = store.Sample{Seq: i, At: t0.Add(time.Duration(i) * time.Second), ElapsedS: float64(i + 1),
			DistanceM: float64(10 * (i + 1)), SpeedKmh: 36, PowerW: 150, CadenceRpm: 80, Level: 5,
			Lat: math.NaN(), Lon: math.NaN(), EleM: math.NaN()}
	}
	end := ss[len(ss)-1].At
	r := store.Ride{ID: 1, RiderID: 1, Mode: "free", StartedAt: t0, EndedAt: &end, Summary: history.Summarize(ss), SampleCount: len(ss)}
	var buf bytes.Buffer
	if err := fitexport.Encode(&buf, r, ss); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
