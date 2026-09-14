package idrac

import (
	"errors"
	"io"
	"sync"
	"time"
)

// socket is the part of a WebSocket connection the transport uses. The
// interface keeps the demultiplexer testable without a network.
type socket interface {
	ReadMessage() ([]byte, error)
	WriteMessage(payload []byte) error
	SetReadDeadline(t time.Time) error
	Close() error
}

// transport turns the console WebSocket into the plain byte stream the rfb
// package expects, pulling Dell's own control messages out on the way.
//
// The firmware sends every control message in a WebSocket message of its own,
// never mixed into RFB data. Even so, a filter that only looked at the leading
// bytes would misread pixel data that happens to start with 247. The guard
// against that is the boundary flag: a message is only considered for control
// routing while the RFB layer sits between protocol messages, which the
// session loop declares before each read.
type transport struct {
	socket  socket
	control func([]byte)

	mu        sync.Mutex
	pending   []byte
	boundary  bool
	handshake bool

	closeOnce sync.Once
}

// SetHandshake marks the phase before the framebuffer starts flowing. No pixel
// data exists yet, so every control message can be routed regardless of where
// the RFB layer stands. The firmware needs this: it sends the client list
// between the version exchange and the security types.
func (t *transport) SetHandshake(active bool) {
	t.mu.Lock()
	t.handshake = active
	t.mu.Unlock()
}

// routeControl reports whether a message should go to the control handler
// rather than into the RFB stream.
func (t *transport) routeControl(message []byte) bool {
	if !IsControlFrame(message) {
		return false
	}
	t.mu.Lock()
	open := t.handshake || t.boundary
	t.mu.Unlock()
	if open {
		return true
	}
	// Power state, boot device and keyboard LEDs arrive whenever the firmware
	// feels like it, including in the middle of a framebuffer update. Those
	// messages have a fixed length, which separates them from pixel data that
	// merely starts with the same two bytes.
	return isFixedControlLength(message)
}

func isFixedControlLength(message []byte) bool {
	switch message[1] {
	case MsgKey2Request:
		return len(message) == 2
	case MsgHostPowerState, MsgFirstBootDevice, MsgKeyboardLEDStatus, MsgFPSData,
		MsgLockdownState, MsgGracefulShutdown:
		return len(message) == 3
	}
	return false
}

func newTransport(conn socket, control func([]byte)) *transport {
	return &transport{socket: conn, control: control}
}

// SetBoundary declares whether the RFB layer currently sits between messages.
func (t *transport) SetBoundary(atBoundary bool) {
	t.mu.Lock()
	t.boundary = atBoundary
	t.mu.Unlock()
}

// push places bytes in front of the stream. The session uses it when the
// firmware skips the key challenge and starts with RFB data.
func (t *transport) push(data []byte) {
	t.mu.Lock()
	t.pending = append(append([]byte(nil), data...), t.pending...)
	t.mu.Unlock()
}

func (t *transport) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		t.mu.Lock()
		if len(t.pending) > 0 {
			n := copy(p, t.pending)
			t.pending = t.pending[n:]
			// Handing out bytes means the RFB layer is inside a message now.
			t.boundary = false
			t.mu.Unlock()
			return n, nil
		}
		t.mu.Unlock()

		message, err := t.socket.ReadMessage()
		if err != nil {
			return 0, err
		}
		if len(message) == 0 {
			continue
		}
		if t.routeControl(message) {
			if t.control != nil {
				t.control(message)
			}
			continue
		}
		t.mu.Lock()
		t.pending = message
		t.mu.Unlock()
	}
}

func (t *transport) Write(p []byte) (int, error) {
	if err := t.socket.WriteMessage(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *transport) SetReadDeadline(deadline time.Time) error {
	return t.socket.SetReadDeadline(deadline)
}

func (t *transport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		err = t.socket.Close()
	})
	return err
}

var _ io.ReadWriter = (*transport)(nil)

// errConsoleClosed reports that the console channel went away.
var errConsoleClosed = errors.New("console connection closed")
