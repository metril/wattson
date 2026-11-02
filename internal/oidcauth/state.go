package oidcauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// stateTTL bounds how long an encoded state value stays valid.
const stateTTL = 5 * time.Minute

// ErrStateInvalid is returned by StateCodec.Decode when a value is
// malformed, tampered with, signed by another key or expired.
var ErrStateInvalid = errors.New("oidcauth: invalid state")

// State is the per-login data kept in the signed state cookie between the
// login redirect and the callback, so the server needs no storage for it.
type State struct {
	Slug     string `json:"slug"`
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"return_to"`
}

// StateCodec signs and verifies State values with HMAC-SHA256.
type StateCodec struct {
	key []byte
}

// NewStateCodec returns a codec keyed with key. Codecs built from the same
// key decode each other's values, so state survives a restart.
func NewStateCodec(key []byte) *StateCodec {
	return &StateCodec{key: append([]byte(nil), key...)}
}

type signedState struct {
	State
	Exp int64 `json:"exp"` // Unix nanoseconds
}

// Encode packs s into a signed token that expires stateTTL after now.
func (c *StateCodec) Encode(s State, now time.Time) string {
	payload, err := json.Marshal(signedState{State: s, Exp: now.Add(stateTTL).UnixNano()})
	if err != nil {
		panic("oidcauth: marshal state: " + err.Error()) // strings only; cannot fail
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(c.sign(payload))
}

// Decode verifies and unpacks a value produced by Encode. Any failure, an
// expired value included, is ErrStateInvalid.
func (c *StateCodec) Decode(v string, now time.Time) (State, error) {
	p, s, ok := strings.Cut(v, ".")
	if !ok {
		return State{}, ErrStateInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return State{}, ErrStateInvalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || !hmac.Equal(sig, c.sign(payload)) {
		return State{}, ErrStateInvalid
	}
	var ss signedState
	if err := json.Unmarshal(payload, &ss); err != nil {
		return State{}, ErrStateInvalid
	}
	if !now.Before(time.Unix(0, ss.Exp)) {
		return State{}, ErrStateInvalid
	}
	return ss.State, nil
}

func (c *StateCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write(payload)
	return mac.Sum(nil)
}
