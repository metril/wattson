package strava

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// stateTTL bounds how long a connect link stays valid.
const stateTTL = 10 * time.Minute

// ErrState reports a connect state that is malformed, tampered with, signed
// by another key, expired, or issued to another user.
var ErrState = errors.New("strava: invalid state")

// ConnectState is what the signed OAuth state carries between the connect
// redirect and the callback, so the server stores nothing.
type ConnectState struct {
	RiderID  int64  `json:"r"`
	UserID   int64  `json:"u"`
	ReturnTo string `json:"t"`
}

type signedConnectState struct {
	ConnectState
	Exp int64 `json:"e"` // Unix nanoseconds
}

// EncodeState signs s with key (HMAC-SHA256) as base64url(payload).base64url(mac)
// and a lifetime of ten minutes from now.
func EncodeState(key []byte, s ConnectState, now time.Time) string {
	payload, err := json.Marshal(signedConnectState{ConnectState: s, Exp: now.Add(stateTTL).UnixNano()})
	if err != nil {
		panic("strava: marshal state: " + err.Error()) // ints and a string: cannot fail
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac(key, payload))
}

// DecodeState verifies and unpacks a value from EncodeState. Every failure,
// an expired value included, is ErrState.
func DecodeState(key []byte, v string, now time.Time) (ConnectState, error) {
	p, m, ok := strings.Cut(v, ".")
	if !ok {
		return ConnectState{}, ErrState
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(p)
	if err != nil {
		return ConnectState{}, ErrState
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(m)
	if err != nil || !hmac.Equal(sig, mac(key, payload)) {
		return ConnectState{}, ErrState
	}
	var ss signedConnectState
	if err := json.Unmarshal(payload, &ss); err != nil || !now.Before(time.Unix(0, ss.Exp)) {
		return ConnectState{}, ErrState
	}
	return ss.ConnectState, nil
}

func mac(key, payload []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(payload)
	return h.Sum(nil)
}
