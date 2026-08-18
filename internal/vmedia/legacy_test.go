package vmedia

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestMarshalLegacyHelloCDROMAndEncryptedSessionKey(t *testing.T) {
	got, err := MarshalLegacyHello(LegacyInfo{
		SessionKey:        "abc",
		EncryptionKeyText: "00112233445566778899aabbccddeeff",
		EncryptSessionKey: true,
		Device:            LegacyDeviceCDROM,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x10 || got[1] != LegacyDeviceCDROM {
		t.Fatalf("legacy hello prefix=%x", got[:2])
	}
	want := []byte{'a' ^ '0', 'b' ^ '0', 'c' ^ '1'}
	if !bytes.Equal(got[2:5], want) {
		t.Fatalf("legacy hello key=%x want=%x", got[2:5], want)
	}
	if !bytes.Equal(got[5:], make([]byte, 29)) {
		t.Fatalf("legacy hello padding=%x", got[5:])
	}
}

func TestMarshalLegacyHelloRejectsBadInput(t *testing.T) {
	if _, err := MarshalLegacyHello(LegacyInfo{SessionKey: "key", Device: 9}); err == nil {
		t.Fatal("expected unsupported device error")
	}
	if _, err := MarshalLegacyHello(LegacyInfo{SessionKey: "key", Device: LegacyDeviceCDROM, EncryptSessionKey: true}); err == nil {
		t.Fatal("expected missing encryption mask error")
	}
	if _, err := MarshalLegacyHello(LegacyInfo{SessionKey: string(bytes.Repeat([]byte{'x'}, 33)), Device: LegacyDeviceCDROM}); err == nil {
		t.Fatal("expected oversized session key error")
	}
}

func TestWriteLegacyHelloHandlesShortWrites(t *testing.T) {
	writer := &legacyShortWriter{limit: 3}
	want := []byte{0x10, 2, 1, 2, 3, 4, 5}
	if err := writeLegacyHello(writer, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(writer.written, want) {
		t.Fatalf("written=%x want=%x", writer.written, want)
	}
}

func TestLegacyMediaConnHandlesShortWrites(t *testing.T) {
	underlying := &legacyShortConn{legacyShortWriter: legacyShortWriter{limit: 2}}
	conn := &legacyMediaConn{Conn: underlying}
	want := []byte{1, 2, 3, 4, 5}
	n, err := conn.Write(want)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) || !bytes.Equal(underlying.written, want) {
		t.Fatalf("Write()=(%d,%x), want=(%d,%x)", n, underlying.written, len(want), want)
	}
}

func TestDialLegacyHandshakeSuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		var hello [34]byte
		if _, err := io.ReadFull(conn, hello[:]); err != nil {
			serverErr <- err
			return
		}
		if hello[0] != 0x10 || hello[1] != LegacyDeviceCDROM || string(bytes.TrimRight(hello[2:], "\x00")) != "session" {
			serverErr <- errors.New("unexpected legacy virtual-media hello")
			return
		}
		_, err = conn.Write([]byte{32, 0, 0, 0})
		serverErr <- err
	}()

	addr := listener.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := DialLegacy(ctx, LegacyInfo{Host: addr.IP.String(), Port: uint16(addr.Port), SessionKey: "session", Device: LegacyDeviceCDROM})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestDialLegacyReturnsTypedStatusError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var hello [34]byte
		_, _ = io.ReadFull(conn, hello[:])
		_, _ = conn.Write([]byte{35, 0, 0, 0})
	}()
	addr := listener.Addr().(*net.TCPAddr)
	_, err = DialLegacy(context.Background(), LegacyInfo{Host: addr.IP.String(), Port: uint16(addr.Port), SessionKey: "session", Device: LegacyDeviceCDROM})
	var statusErr LegacyHandshakeError
	if !errors.As(err, &statusErr) || statusErr.Code != 35 {
		t.Fatalf("DialLegacy error=%#v", err)
	}
}

type legacyShortWriter struct {
	limit   int
	written []byte
}

func (w *legacyShortWriter) Write(p []byte) (int, error) {
	n := min(w.limit, len(p))
	w.written = append(w.written, p[:n]...)
	return n, nil
}

type legacyShortConn struct {
	legacyShortWriter
}

func (*legacyShortConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (*legacyShortConn) Close() error                     { return nil }
func (*legacyShortConn) LocalAddr() net.Addr              { return nil }
func (*legacyShortConn) RemoteAddr() net.Addr             { return nil }
func (*legacyShortConn) SetDeadline(time.Time) error      { return nil }
func (*legacyShortConn) SetReadDeadline(time.Time) error  { return nil }
func (*legacyShortConn) SetWriteDeadline(time.Time) error { return nil }
