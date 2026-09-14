package idrac

import (
	"context"
	"testing"

	"firstlight/internal/rfb"
)

// keyboardFixture builds the minimum of a session needed to drive the remote
// keyboard: the transport, the RFB writer and a connected state.
func keyboardFixture(t *testing.T) (*Session, *scriptedWS) {
	t.Helper()
	socket := &scriptedWS{}
	s := &Session{done: make(chan struct{}), notify: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	t.Cleanup(s.cancel)
	s.trans = newTransport(socket, nil)
	s.rfb = rfb.New(s.trans)
	s.state = State{Connected: true, InputReady: true}
	return s, socket
}

func TestReleaseHeldKeysSendsTheKeyUp(t *testing.T) {
	s, socket := keyboardFixture(t)
	// Hold the letter a, HID usage 4.
	if err := s.SendKeyboardReport(kvmReport(4)); err != nil {
		t.Fatalf("press: %v", err)
	}
	pressed := len(socket.written)
	if pressed == 0 {
		t.Fatal("the key press never reached the socket")
	}

	s.releaseHeldKeys()

	if len(socket.written) <= pressed {
		t.Fatal("no key release was sent while the session was still open")
	}
}

func TestReleaseHeldKeysNeedsToRunBeforeClosed(t *testing.T) {
	s, socket := keyboardFixture(t)
	if err := s.SendKeyboardReport(kvmReport(4)); err != nil {
		t.Fatalf("press: %v", err)
	}
	pressed := len(socket.written)

	// This is the order Close used to run in. Every send is refused once the
	// session is marked closed, so the release was dropped on the floor.
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.releaseHeldKeys()

	if len(socket.written) != pressed {
		t.Fatal("a closed session still wrote to the socket")
	}
}

// kvmReport builds the HID report shape the iLO path uses, which is what
// SendKeyboardReport takes on the Dell side as well.
func kvmReport(keys ...byte) [10]byte {
	var report [10]byte
	report[0] = 1
	for i, key := range keys {
		if i >= 6 {
			break
		}
		report[4+i] = key
	}
	return report
}
