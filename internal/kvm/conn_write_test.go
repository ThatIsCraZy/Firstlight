package kvm

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestWriteIsBoundedAndThenRefusesTheConnection(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })

	previous := writeTimeout
	writeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { writeTimeout = previous })

	c := &Conn{net: client}
	// Nothing reads the far end, which is what a half-open socket looks like.
	start := time.Now()
	if _, err := c.Write([]byte{5, 0}); err == nil {
		t.Fatal("a write nobody was reading reported success")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the write took %s, so it was not bounded", elapsed)
	}

	// The cipher stream has moved on past bytes the peer never saw, so the
	// connection cannot be used again.
	if _, err := c.Write([]byte{5, 0}); !errors.Is(err, ErrWriteBroken) {
		t.Fatalf("second write error = %v, want ErrWriteBroken", err)
	}
}

func TestWriteLeavesAHealthyConnectionUsable(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }()

	c := &Conn{net: client}
	for i := range 3 {
		if _, err := c.Write([]byte{5, 0}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
}
