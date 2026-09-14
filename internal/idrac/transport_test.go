package idrac

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// scriptedWS replays a fixed list of WebSocket messages and records what the
// client wrote. It stands in for wsx.Conn, which transport only uses through
// these three methods.
type scriptedWS struct {
	mu       sync.Mutex
	messages [][]byte
	index    int
	written  [][]byte
	closed   bool
}

func (s *scriptedWS) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *scriptedWS) ReadMessage() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.messages) {
		return nil, io.EOF
	}
	message := s.messages[s.index]
	s.index++
	return message, nil
}

func (s *scriptedWS) WriteMessage(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written = append(s.written, append([]byte(nil), p...))
	return nil
}

func (s *scriptedWS) SetReadDeadline(time.Time) error { return nil }

func (s *scriptedWS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func newScriptedTransport(conn *scriptedWS, control func([]byte)) *transport {
	return newTransport(conn, control)
}

func TestTransportRoutesControlFramesAtMessageBoundary(t *testing.T) {
	socket := &scriptedWS{messages: [][]byte{
		{247, MsgHostPowerState, 1},
		[]byte("RFB 003.008\n"),
	}}
	var seen [][]byte
	trans := newScriptedTransport(socket, func(frame []byte) {
		seen = append(seen, append([]byte(nil), frame...))
	})
	trans.SetBoundary(true)

	got := make([]byte, 12)
	if _, err := io.ReadFull(trans, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != "RFB 003.008\n" {
		t.Fatalf("RFB bytes = %q", got)
	}
	if len(seen) != 1 || seen[0][1] != MsgHostPowerState {
		t.Fatalf("control frames = %v, want one power state message", seen)
	}
}

// Pixel data may legitimately start with the bytes that identify a control
// message. Inside a protocol message the transport must pass it through.
func TestTransportTreatsControlLookalikeAsDataInsideAMessage(t *testing.T) {
	payload := []byte{247, MsgHostPowerState, 0x42, 0x43}
	socket := &scriptedWS{messages: [][]byte{
		{0, 0, 0, 1}, // start of a framebuffer update
		payload,      // raw pixels that look like a control frame
	}}
	var seen int
	trans := newScriptedTransport(socket, func([]byte) { seen++ })
	trans.SetBoundary(true)

	head := make([]byte, 4)
	if _, err := io.ReadFull(trans, head); err != nil {
		t.Fatalf("ReadFull header: %v", err)
	}
	// The session loop does not declare a new boundary here, because it is in
	// the middle of decoding the update.
	body := make([]byte, len(payload))
	if _, err := io.ReadFull(trans, body); err != nil {
		t.Fatalf("ReadFull body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body = % x, want % x", body, payload)
	}
	if seen != 0 {
		t.Fatalf("control handler ran %d times, want none", seen)
	}
}

func TestTransportSkipsEmptyMessages(t *testing.T) {
	socket := &scriptedWS{messages: [][]byte{nil, {}, []byte("ok")}}
	trans := newScriptedTransport(socket, nil)
	trans.SetBoundary(true)
	got := make([]byte, 2)
	if _, err := io.ReadFull(trans, got); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("payload = %q", got)
	}
}

func TestTransportReportsReadErrors(t *testing.T) {
	trans := newScriptedTransport(&scriptedWS{}, nil)
	trans.SetBoundary(true)
	if _, err := trans.Read(make([]byte, 4)); !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v, want EOF", err)
	}
}

func TestTransportWriteSendsOneMessage(t *testing.T) {
	socket := &scriptedWS{}
	trans := newScriptedTransport(socket, nil)
	n, err := trans.Write([]byte{1, 2, 3})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 3 {
		t.Fatalf("n = %d, want 3", n)
	}
	if len(socket.written) != 1 || !bytes.Equal(socket.written[0], []byte{1, 2, 3}) {
		t.Fatalf("written = %v", socket.written)
	}
}

// Several short reads must drain one message before the next is pulled.
func TestTransportDeliversOneMessageAcrossSeveralReads(t *testing.T) {
	socket := &scriptedWS{messages: [][]byte{[]byte("abcdef")}}
	trans := newScriptedTransport(socket, nil)
	trans.SetBoundary(true)
	first := make([]byte, 2)
	if _, err := trans.Read(first); err != nil {
		t.Fatalf("first read: %v", err)
	}
	rest := make([]byte, 4)
	if _, err := io.ReadFull(trans, rest); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(first)+string(rest) != "abcdef" {
		t.Fatalf("payload = %q%q", first, rest)
	}
}
