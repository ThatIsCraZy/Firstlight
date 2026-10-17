package wsx

import (
	"errors"
	"io"
	"testing"
	"time"
)

// A controller may still be processing the final vendor command when it
// receives Close. Keep the transport alive until it can acknowledge Close.
func TestCloseWaitsForPeerHandshake(t *testing.T) {
	for _, reading := range []bool{false, true} {
		name := "no reader"
		if reading {
			name = "active reader"
		}
		t.Run(name, func(t *testing.T) {
			conn, server := pair(t)
			_ = server.SetDeadline(time.Now().Add(3 * time.Second))
			peer := newConn(server)
			readDone := make(chan error, 1)
			if reading {
				go func() {
					_, err := conn.ReadMessage()
					readDone <- err
				}()
				// The pong proves ReadMessage is already running before Close.
				if _, err := server.Write(serverFrame(true, opPing, nil)); err != nil {
					t.Fatal(err)
				}
				if pong, err := peer.readFrame(); err != nil || pong.opcode != opPong {
					t.Fatalf("reader readiness: opcode=%d, err=%v", pong.opcode, err)
				}
			}
			closed := make(chan error, 1)
			go func() { closed <- conn.Close() }()
			frame, err := peer.readFrame()
			if err != nil || frame.opcode != opClose {
				t.Fatalf("client close frame: opcode=%d, err=%v", frame.opcode, err)
			}
			if _, err := server.Write(serverFrame(true, opClose, []byte{0x03, 0xe8})); err != nil {
				t.Fatalf("transport closed before the peer could acknowledge Close: %v", err)
			}
			if err := <-closed; err != nil {
				t.Fatalf("Close: %v", err)
			}
			if reading {
				if err := <-readDone; !errors.Is(err, ErrClosed) {
					t.Fatalf("reader error = %v, want ErrClosed", err)
				}
			}
			if _, err := server.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Fatalf("after close acknowledgement: %v, want EOF without a second close frame", err)
			}
		})
	}
}

func TestCloseDrainsInFlightDataBeforePeerAcknowledgement(t *testing.T) {
	conn, server := pair(t)
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	if _, err := newConn(server).readFrame(); err != nil {
		t.Fatal(err)
	}
	for _, frame := range [][]byte{
		serverFrame(true, opBinary, []byte("final framebuffer update")),
		serverFrame(true, opClose, []byte{0x03, 0xe8}),
	} {
		if _, err := server.Write(frame); err != nil {
			t.Fatalf("transport closed before the closing handshake completed: %v", err)
		}
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestCloseRejectsFurtherDataFrames(t *testing.T) {
	conn, server := pair(t)
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	if _, err := newConn(server).readFrame(); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage([]byte("late framebuffer request")); !errors.Is(err, ErrClosed) {
		t.Errorf("write after Close = %v, want ErrClosed", err)
	}
	_, _ = server.Write(serverFrame(true, opClose, []byte{0x03, 0xe8}))
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestCloseDoesNotWaitForeverForSilentPeer(t *testing.T) {
	conn, server := pair(t)
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	if _, err := newConn(server).readFrame(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not time out waiting for a silent peer")
	}
}
