package esphomeapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/flynn/noise"
)

var (
	// ErrBadKey means the server rejected (or failed to authenticate) our PSK.
	ErrBadKey = errors.New("esphomeapi: invalid encryption key")
	// ErrHandshake means the Noise handshake failed for a non-key reason.
	ErrHandshake = errors.New("esphomeapi: handshake failed")
)

var (
	noiseSuite    = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
	noisePrologue = []byte("NoiseAPIInit\x00\x00")
	noiseHello    = []byte{preambleNoise, 0x00, 0x00}
)

// errTooLarge marks a message rejected before anything was written, so the
// stream is still intact.
var errTooLarge = errors.New("esphomeapi: message too large")

// ServerHello is the identity the device announces before the handshake.
type ServerHello struct{ Name, MAC string }

// secureConn is a Noise-transport ESPHome connection after a completed handshake.
type secureConn struct {
	c    net.Conn
	wmu  sync.Mutex
	send *noise.CipherState
	rmu  sync.Mutex
	recv *noise.CipherState

	// wtimeout bounds each WriteMsg so a stalled peer cannot block a writer
	// forever. Set before the conn is shared; zero disables the deadline.
	wtimeout time.Duration
}

// clientHandshake performs the ESPHome Noise_NNpsk0_25519_ChaChaPoly_SHA256
// handshake as initiator. ctx bounds the whole handshake.
func clientHandshake(ctx context.Context, c net.Conn, psk []byte) (*secureConn, ServerHello, error) {
	var hello ServerHello
	if len(psk) != 32 {
		return nil, hello, fmt.Errorf("%w: psk must be 32 bytes, got %d", ErrBadKey, len(psk))
	}
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noiseSuite,
		Pattern:               noise.HandshakeNN,
		Initiator:             true,
		Prologue:              noisePrologue,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, hello, fmt.Errorf("%w: %v", ErrHandshake, err)
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	defer func() {
		stop()
		_ = c.SetDeadline(time.Time{})
	}()
	fail := func(err error) (*secureConn, ServerHello, error) {
		// The conn deadline can fire marginally before ctx.Err() is set.
		cerr := ctx.Err()
		if cerr == nil && errors.Is(err, os.ErrDeadlineExceeded) {
			cerr = context.DeadlineExceeded
		}
		if cerr != nil {
			err = fmt.Errorf("%w: %w (%v)", ErrHandshake, cerr, err)
		}
		return nil, ServerHello{}, err
	}

	msg1, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrHandshake, err))
	}
	out := make([]byte, 0, len(noiseHello)+4+len(msg1))
	out = append(out, noiseHello...)
	out = append(out, preambleNoise, byte((len(msg1)+1)>>8), byte(len(msg1)+1), 0x00)
	out = append(out, msg1...)
	if _, err := c.Write(out); err != nil {
		return fail(fmt.Errorf("%w: send hello: %w", ErrHandshake, err))
	}

	hf, err := readFrame(c)
	if err != nil {
		return fail(fmt.Errorf("%w: read server hello: %w", ErrHandshake, err))
	}
	if hello, err = parseServerHello(hf); err != nil {
		return fail(err)
	}

	rf, err := readFrame(c)
	if err != nil {
		return fail(fmt.Errorf("%w: read handshake reply: %w", ErrHandshake, err))
	}
	if len(rf) == 0 {
		return fail(fmt.Errorf("%w: empty handshake frame", ErrHandshake))
	}
	if rf[0] != 0x00 {
		if string(rf[1:]) == "Handshake MAC failure" {
			return fail(ErrBadKey)
		}
		return fail(fmt.Errorf("%w: server: %s", ErrHandshake, safeLabel(rf[1:], maxExplanationLen)))
	}
	_, cs0, cs1, err := hs.ReadMessage(nil, rf[1:])
	if err != nil {
		// AEAD failure on the reply means the PSK differs.
		return fail(fmt.Errorf("%w: %v", ErrBadKey, err))
	}
	if cs0 == nil || cs1 == nil {
		return fail(fmt.Errorf("%w: handshake incomplete", ErrHandshake))
	}
	if !stop() {
		// ctx fired while we were finishing; the conn deadline may have been
		// poisoned, so the connection is unusable.
		return fail(ctx.Err())
	}
	return &secureConn{c: c, send: cs0, recv: cs1}, hello, nil
}

// Caps on peer-supplied labels, matching aioesphomeapi's _sanitize module.
const (
	maxNameLen        = 32
	maxMACLen         = 16
	maxExplanationLen = 64
)

// safeLabel strips non-printable characters and caps the length (in runes) of
// a peer-supplied label so a hostile device cannot inject terminal escapes or
// oversized values into logs.
func safeLabel(raw []byte, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range string(raw) { // invalid UTF-8 decodes to U+FFFD, which is printable
		if !unicode.IsPrint(r) {
			continue
		}
		if n == limit {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func parseServerHello(f []byte) (ServerHello, error) {
	var h ServerHello
	if len(f) == 0 {
		return h, fmt.Errorf("%w: empty server hello", ErrHandshake)
	}
	if f[0] != 0x01 {
		return h, fmt.Errorf("%w: unknown noise protocol 0x%02x", ErrHandshake, f[0])
	}
	rest := f[1:]
	name, rest, ok := bytes.Cut(rest, []byte{0})
	if !ok { // pre-2022.2 firmware: no name
		return h, nil
	}
	h.Name = safeLabel(name, maxNameLen)
	if mac, _, ok := bytes.Cut(rest, []byte{0}); ok { // mac added in 2025.4
		h.MAC = safeLabel(mac, maxMACLen)
	}
	return h, nil
}

// WriteMsg encrypts and sends one API message, failing after the conn's write timeout.
func (s *secureConn) WriteMsg(typ uint16, payload []byte) error {
	return s.writeMsg(typ, payload, s.wtimeout)
}

// writeMsg is WriteMsg with an explicit write deadline (0 = none).
func (s *secureConn) writeMsg(typ uint16, payload []byte, d time.Duration) error {
	if len(payload)+4+16 > maxFrame {
		return fmt.Errorf("%w: %d bytes", errTooLarge, len(payload))
	}
	pt := make([]byte, 4+len(payload))
	pt[0], pt[1] = byte(typ>>8), byte(typ)
	pt[2], pt[3] = byte(len(payload)>>8), byte(len(payload))
	copy(pt[4:], payload)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	ct, err := s.send.Encrypt(nil, nil, pt)
	if err != nil {
		return err
	}
	if d > 0 {
		_ = s.c.SetWriteDeadline(time.Now().Add(d))
	}
	return writeFrame(s.c, ct)
}

// ReadMsg reads and decrypts one API message.
func (s *secureConn) ReadMsg() (uint16, []byte, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	f, err := readFrame(s.c)
	if err != nil {
		return 0, nil, err
	}
	pt, err := s.recv.Decrypt(nil, nil, f)
	if err != nil {
		return 0, nil, fmt.Errorf("esphomeapi: decrypt: %w", err)
	}
	if len(pt) < 4 {
		return 0, nil, fmt.Errorf("esphomeapi: decrypted message too short: %d bytes", len(pt))
	}
	// The length field is untrusted; use the actual remainder like aioesphomeapi.
	return uint16(pt[0])<<8 | uint16(pt[1]), pt[4:], nil
}

// Close closes the underlying connection.
func (s *secureConn) Close() error { return s.c.Close() }
