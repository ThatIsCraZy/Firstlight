package wsx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestAcceptKeyMatchesRFC6455Example(t *testing.T) {
	if got, want := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="), "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="; got != want {
		t.Fatalf("acceptKey = %q, want %q", got, want)
	}
}

// serverFrame builds an unmasked frame the way a server sends it.
func serverFrame(fin bool, opcode byte, payload []byte) []byte {
	first := opcode
	if fin {
		first |= 0x80
	}
	out := []byte{first}
	n := len(payload)
	switch {
	case n < 126:
		out = append(out, byte(n))
	case n <= 0xffff:
		out = append(out, 126, byte(n>>8), byte(n))
	default:
		out = append(out, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		out = append(out, ext[:]...)
	}
	return append(out, payload...)
}

// pair returns a client Conn and the server side of the pipe.
func pair(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() {
		_ = clientSide.Close()
		_ = serverSide.Close()
	})
	return newConn(clientSide), serverSide
}

func TestReadMessageReturnsSingleBinaryFrame(t *testing.T) {
	conn, server := pair(t)
	go func() {
		_, _ = server.Write(serverFrame(true, opBinary, []byte("hello")))
	}()
	got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("payload = %q, want %q", got, "hello")
	}
}

func TestReadMessageReassemblesFragments(t *testing.T) {
	conn, server := pair(t)
	go func() {
		_, _ = server.Write(serverFrame(false, opBinary, []byte("RFB ")))
		_, _ = server.Write(serverFrame(false, opContinuation, []byte("003.")))
		_, _ = server.Write(serverFrame(true, opContinuation, []byte("008\n")))
	}()
	got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "RFB 003.008\n" {
		t.Fatalf("payload = %q", got)
	}
}

// An empty first fragment must still open a fragmented message, otherwise the
// following continuation frame looks like a stray one.
func TestReadMessageHandlesEmptyFirstFragment(t *testing.T) {
	conn, server := pair(t)
	go func() {
		_, _ = server.Write(serverFrame(false, opBinary, nil))
		_, _ = server.Write(serverFrame(true, opContinuation, []byte("data")))
	}()
	got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "data" {
		t.Fatalf("payload = %q, want %q", got, "data")
	}
}

func TestReadMessageAnswersPingBeforeReturningData(t *testing.T) {
	conn, server := pair(t)
	done := make(chan []byte, 1)
	go func() {
		_, _ = server.Write(serverFrame(true, opPing, []byte("probe")))
		reply := make([]byte, 64)
		n, _ := server.Read(reply)
		done <- reply[:n]
		_, _ = server.Write(serverFrame(true, opBinary, []byte("payload")))
	}()
	got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("payload = %q", got)
	}
	pong := <-done
	if len(pong) < 2 || pong[0]&0x0f != opPong {
		t.Fatalf("expected a pong frame, got % x", pong)
	}
	if pong[1]&0x80 == 0 {
		t.Fatal("client frames must be masked")
	}
	// Unmask and compare the echoed payload.
	mask := pong[2:6]
	body := append([]byte(nil), pong[6:]...)
	for i := range body {
		body[i] ^= mask[i%4]
	}
	if string(body) != "probe" {
		t.Fatalf("pong payload = %q, want %q", body, "probe")
	}
}

func TestReadMessageReportsCloseFrame(t *testing.T) {
	conn, server := pair(t)
	go func() {
		payload := append([]byte{0x03, 0xe8}, []byte("bye")...)
		_, _ = server.Write(serverFrame(true, opClose, payload))
		// Drain the echoed close so the pipe write above does not block.
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		_, _ = server.Read(make([]byte, 64))
	}()
	_, err := conn.ReadMessage()
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("error = %v, want ErrClosed", err)
	}
}

func TestReadFrameRejectsFragmentedControlFrame(t *testing.T) {
	conn, server := pair(t)
	go func() {
		_, _ = server.Write(serverFrame(false, opPing, []byte("x")))
	}()
	if _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected an error for a fragmented control frame")
	}
}

func TestReadFrameRejectsReservedBits(t *testing.T) {
	conn, server := pair(t)
	go func() {
		raw := serverFrame(true, opBinary, []byte("x"))
		raw[0] |= 0x40 // RSV1 without a negotiated extension
		_, _ = server.Write(raw)
	}()
	if _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected an error for reserved bits")
	}
}

func TestWriteMessageMasksPayload(t *testing.T) {
	conn, server := pair(t)
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := server.Read(buf)
		got <- buf[:n]
	}()
	if err := conn.WriteMessage([]byte("abcd")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	raw := <-got
	if raw[0] != 0x80|opBinary {
		t.Fatalf("first byte = %#x", raw[0])
	}
	if raw[1] != 0x80|4 {
		t.Fatalf("second byte = %#x, want masked length 4", raw[1])
	}
	mask := raw[2:6]
	body := append([]byte(nil), raw[6:]...)
	for i := range body {
		body[i] ^= mask[i%4]
	}
	if !bytes.Equal(body, []byte("abcd")) {
		t.Fatalf("payload = %q", body)
	}
}

// A 16-bit length header must round trip, since framebuffer updates routinely
// exceed 125 bytes.
func TestWriteMessageUsesExtendedLength(t *testing.T) {
	conn, server := pair(t)
	got := make(chan []byte, 1)
	payload := bytes.Repeat([]byte{0xab}, 200)
	go func() {
		buf := make([]byte, 512)
		n, _ := server.Read(buf)
		got <- buf[:n]
	}()
	if err := conn.WriteMessage(payload); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	raw := <-got
	if raw[1]&0x7f != 126 {
		t.Fatalf("length marker = %d, want 126", raw[1]&0x7f)
	}
	if n := binary.BigEndian.Uint16(raw[2:4]); int(n) != len(payload) {
		t.Fatalf("declared length = %d, want %d", n, len(payload))
	}
}
