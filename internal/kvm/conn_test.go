package kvm

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestConnWriteReportsShortUnderlyingWrite(t *testing.T) {
	underlying := &shortWriteConn{limit: 2}
	conn := &Conn{net: underlying}
	input := []byte{1, 2, 3, 4}
	n, err := conn.Write(input)
	if n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write()=(%d,%v), want (2,%v)", n, err, io.ErrShortWrite)
	}
	if len(underlying.written) != 2 || underlying.written[0] != 1 || underlying.written[1] != 2 {
		t.Fatalf("underlying bytes=%x", underlying.written)
	}
}

func TestInfoNetworkAddressSupportsHostnamesAndIPv6(t *testing.T) {
	tests := []struct {
		info Info
		want string
	}{
		{info: Info{Host: "ilo.example", Port: 17990}, want: "ilo.example:17990"},
		{info: Info{Host: "2001:db8::1", Port: 17990}, want: "[2001:db8::1]:17990"},
	}
	for _, test := range tests {
		if got := test.info.networkAddress(); got != test.want {
			t.Fatalf("networkAddress()=%q want=%q", got, test.want)
		}
	}
}

type shortWriteConn struct {
	limit   int
	written []byte
}

func (c *shortWriteConn) Write(p []byte) (int, error) {
	n := min(c.limit, len(p))
	c.written = append(c.written, p[:n]...)
	return n, nil
}

func (*shortWriteConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*shortWriteConn) Close() error                     { return nil }
func (*shortWriteConn) LocalAddr() net.Addr              { return nil }
func (*shortWriteConn) RemoteAddr() net.Addr             { return nil }
func (*shortWriteConn) SetDeadline(time.Time) error      { return nil }
func (*shortWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (*shortWriteConn) SetWriteDeadline(time.Time) error { return nil }
