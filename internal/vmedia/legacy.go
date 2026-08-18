package vmedia

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	LegacyDeviceFloppy byte = 1
	LegacyDeviceCDROM  byte = 2
	LegacyDeviceUSBKey byte = 3
)

type LegacyInfo struct {
	Host              string
	Port              uint16
	SessionKey        string
	EncryptionKeyText string
	EncryptSessionKey bool
	Device            byte
}

type LegacyHandshakeError struct {
	Code byte
}

func (e LegacyHandshakeError) Error() string {
	switch e.Code {
	case 33:
		return "legacy virtual-media drive is already connected"
	case 34:
		return "legacy virtual-media session key is invalid"
	case 35:
		return "legacy virtual media is not licensed"
	case 36:
		return "legacy virtual-media session key has expired"
	case 37:
		return "legacy virtual-media drive is not configured"
	case 38:
		return "legacy virtual media requires additional permission"
	default:
		return fmt.Sprintf("legacy virtual-media handshake failed with status %d", e.Code)
	}
}

func DialLegacy(ctx context.Context, info LegacyInfo) (Conn, error) {
	if info.Port == 0 {
		return nil, fmt.Errorf("legacy virtual-media port is required")
	}
	request, err := MarshalLegacyHello(info)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(info.Host, strconv.Itoa(int(info.Port))))
	if err != nil {
		return nil, err
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	deadline := time.Now().Add(10 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := writeLegacyHello(conn, request[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if reply[0] != 32 || reply[1] != 0 {
		_ = conn.Close()
		return nil, LegacyHandshakeError{Code: reply[0]}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &legacyMediaConn{Conn: conn}, nil
}

type legacyMediaConn struct {
	net.Conn
}

func (c *legacyMediaConn) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n, err := c.Conn.Write(data)
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n <= 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func writeLegacyHello(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func MarshalLegacyHello(info LegacyInfo) ([34]byte, error) {
	var request [34]byte
	if info.Device < LegacyDeviceFloppy || info.Device > LegacyDeviceUSBKey {
		return request, fmt.Errorf("unsupported legacy virtual-media device %d", info.Device)
	}
	key := []byte(info.SessionKey)
	if len(key) > 32 {
		return request, fmt.Errorf("legacy virtual-media session key must be at most 32 bytes, got %d", len(key))
	}
	if info.EncryptSessionKey {
		mask := []byte(info.EncryptionKeyText)
		if len(mask) == 0 {
			return request, fmt.Errorf("legacy ENCRYPT_VMKEY requires the original enc_key text")
		}
		for i := range key {
			key[i] ^= mask[i%len(mask)]
		}
	}
	request[0] = 0x10
	request[1] = info.Device
	copy(request[2:], key)
	return request, nil
}
