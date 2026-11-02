// Package esphometest provides a fake ESPHome native-API server (Noise
// responder) for tests. It is an independent server-side implementation of the
// wire format used by aioesphomeapi, not a mirror of the client code.
package esphometest

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/flynn/noise"
)

// Identity announced in the server hello.
const (
	Name = "wattson-proxy"
	MAC  = "AA:BB:CC:DD:EE:FF"
	// HelloMAC is the MAC as sent in the Noise server hello (12 lowercase hex, no separators).
	HelloMAC = "aabbccddeeff"
)

// Reply is one message sent back by a Handle callback.
type Reply struct {
	Type    uint16
	Payload []byte
}

type session struct {
	c    net.Conn
	wmu  sync.Mutex
	send *noise.CipherState
}

func (s *session) write(typ uint16, payload []byte) error {
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
	return writeFrame(s.c, ct)
}

// Server is a fake ESPHome device. It serves one client at a time.
type Server struct {
	t    testing.TB
	ln   net.Listener
	key  []byte
	wg   sync.WaitGroup
	once sync.Once

	mu       sync.Mutex
	handlers map[uint16]func([]byte) []Reply
	cur      *session
	active   net.Conn      // accepted conn, tracked from accept so shutdown can close it mid-handshake
	ready    chan struct{} // closed once cur is set; replaced when cur clears
	closed   bool
}

// Start listens on 127.0.0.1:0 and serves until the test ends.
func Start(t testing.TB, key []byte) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("esphometest: listen: %v", err)
	}
	s := &Server{
		t: t, ln: ln, key: append([]byte(nil), key...),
		handlers: map[uint16]func([]byte) []Reply{},
		ready:    make(chan struct{}),
	}
	s.wg.Add(1)
	go s.acceptLoop()
	t.Cleanup(s.Close)
	return s
}

// Addr is the host:port the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Handle registers fn for incoming messages of type typ; its replies are sent
// back in order.
func (s *Server) Handle(typ uint16, fn func(payload []byte) []Reply) {
	s.mu.Lock()
	s.handlers[typ] = fn
	s.mu.Unlock()
}

// Push sends an unsolicited message to the connected client, waiting briefly
// for one to complete its handshake.
func (s *Server) Push(typ uint16, payload []byte) {
	s.t.Helper()
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		s.t.Errorf("esphometest: Push: no client connected")
		return
	}
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()
	if cur == nil {
		s.t.Errorf("esphometest: Push: client went away")
		return
	}
	if err := cur.write(typ, payload); err != nil {
		s.t.Errorf("esphometest: Push: %v", err)
	}
}

// CloseClient drops the current client connection, if any.
func (s *Server) CloseClient() {
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()
	if cur != nil {
		_ = cur.c.Close()
	}
}

// Close stops the server and drops any client; it is idempotent and also runs
// automatically at test cleanup.
func (s *Server) Close() { s.once.Do(s.shutdown) }

func (s *Server) shutdown() {
	s.mu.Lock()
	s.closed = true
	active := s.active
	s.mu.Unlock()
	_ = s.ln.Close()
	if active != nil {
		_ = active.Close()
	}
	s.CloseClient()
	s.wg.Wait()
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.active = c
		s.mu.Unlock()
		s.serve(c)
		s.mu.Lock()
		s.active = nil
		s.mu.Unlock()
	}
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	// Make sure a client that connects during shutdown cannot hang us.
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	sess, err := s.handshake(c)
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	recv := sess.recv
	s.mu.Lock()
	s.cur = sess.session
	close(s.ready)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cur = nil
		s.ready = make(chan struct{})
		s.mu.Unlock()
	}()
	for {
		f, err := readFrame(c)
		if err != nil {
			return
		}
		pt, err := recv.Decrypt(nil, nil, f)
		if err != nil || len(pt) < 4 {
			return
		}
		typ := uint16(pt[0])<<8 | uint16(pt[1])
		s.mu.Lock()
		fn := s.handlers[typ]
		s.mu.Unlock()
		if fn == nil {
			continue
		}
		for _, r := range fn(pt[4:]) {
			if err := sess.write(r.Type, r.Payload); err != nil {
				return
			}
		}
	}
}

type serverSession struct {
	*session
	recv *noise.CipherState
}

func (s *Server) handshake(c net.Conn) (*serverSession, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Pattern:               noise.HandshakeNN,
		Initiator:             false,
		Prologue:              []byte("NoiseAPIInit\x00\x00"),
		PresharedKey:          s.key,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return nil, err
	}
	// Client hello: an empty noise frame.
	if _, err := readFrame(c); err != nil {
		return nil, err
	}
	// Server hello: 01 name\0 mac\0
	hello := append([]byte{0x01}, Name...)
	hello = append(hello, 0)
	hello = append(hello, HelloMAC...)
	hello = append(hello, 0)
	if err := writeFrame(c, hello); err != nil {
		return nil, err
	}
	hf, err := readFrame(c)
	if err != nil {
		return nil, err
	}
	if len(hf) == 0 || hf[0] != 0x00 {
		_ = writeFrame(c, append([]byte{0x01}, "Bad handshake"...))
		return nil, errors.New("bad handshake frame")
	}
	if _, _, _, err := hs.ReadMessage(nil, hf[1:]); err != nil {
		_ = writeFrame(c, append([]byte{0x01}, "Handshake MAC failure"...))
		return nil, err
	}
	msg2, cs0, cs1, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(c, append([]byte{0x00}, msg2...)); err != nil {
		return nil, err
	}
	// Responder: cs0 decrypts initiator->responder, cs1 encrypts responses.
	return &serverSession{session: &session{c: c, send: cs1}, recv: cs0}, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	buf := make([]byte, 3+len(payload))
	buf[0] = 0x01
	buf[1], buf[2] = byte(len(payload)>>8), byte(len(payload))
	copy(buf[3:], payload)
	_, err := w.Write(buf)
	return err
}

func readFrame(r io.Reader) ([]byte, error) {
	var h [3]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	if h[0] != 0x01 {
		return nil, fmt.Errorf("bad preamble 0x%02x", h[0])
	}
	p := make([]byte, int(h[1])<<8|int(h[2]))
	_, err := io.ReadFull(r, p)
	return p, err
}
