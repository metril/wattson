package oidcauth

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

var testKey = bytes.Repeat([]byte{9}, 32)

func sampleState() State {
	return State{Slug: "authentik", State: "st", Nonce: "no", Verifier: "ve", ReturnTo: "/settings?tab=bike"}
}

func TestStateRoundTrip(t *testing.T) {
	t.Parallel()
	c := NewStateCodec(testKey)
	now := time.Now()
	got, err := c.Decode(c.Encode(sampleState(), now), now)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != sampleState() {
		t.Fatalf("got %+v", got)
	}
}

func TestStateTamper(t *testing.T) {
	t.Parallel()
	c := NewStateCodec(testKey)
	now := time.Now()
	enc := c.Encode(sampleState(), now)
	for name, v := range map[string]string{
		"sig":       enc[:len(enc)-2] + "xx",
		"payload":   "A" + enc[1:],
		"empty":     "",
		"no dot":    "abcdef",
		"junk":      "!!!.!!!",
		"other key": NewStateCodec(bytes.Repeat([]byte{1}, 32)).Encode(sampleState(), now),
	} {
		if _, err := c.Decode(v, now); !errors.Is(err, ErrStateInvalid) {
			t.Errorf("%s: err = %v, want ErrStateInvalid", name, err)
		}
	}
}

func TestStateExpired(t *testing.T) {
	t.Parallel()
	c := NewStateCodec(testKey)
	now := time.Now()
	enc := c.Encode(sampleState(), now)
	if _, err := c.Decode(enc, now.Add(4*time.Minute+59*time.Second)); err != nil {
		t.Fatalf("at 4m59s: %v", err)
	}
	if _, err := c.Decode(enc, now.Add(5*time.Minute)); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("at 5m: err = %v, want ErrStateInvalid", err)
	}
}

func TestStateStableAcrossCodecs(t *testing.T) {
	t.Parallel()
	a, b := NewStateCodec(testKey), NewStateCodec(append([]byte(nil), testKey...))
	now := time.Now()
	got, err := b.Decode(a.Encode(sampleState(), now), now)
	if err != nil || got != sampleState() {
		t.Fatalf("got %+v, %v", got, err)
	}
}
