package kvm

import (
	"context"
	"crypto/cipher"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

type Info struct {
	Host            string
	Port            uint16
	SessionKey      string
	ProtocolVersion int
	Command         Command
	Channel         Channel
	Legacy          *LegacyOptions
}

type LegacyOptions struct {
	EncryptionKey     []byte
	EncryptionKeyText string
	CommandKey        []byte
	EncryptSessionKey bool
	EncryptVMKey      bool
	EncryptCommand    bool
}

type Conn struct {
	net          net.Conn
	encryptor    cipher.Stream
	decryptor    cipher.Stream
	readMu       sync.Mutex
	writeMu      sync.Mutex
	closeOnce    sync.Once
	closeErr     error
	legacyKVM    bool
	legacyKey    []byte
	legacyCipher LegacyCipher
	legacyShared bool
	writeBroken  bool
}

// writeTimeout bounds a single send. A four-octet input record needs no time at
// all, so exceeding this means the socket is half open rather than busy. It is
// a variable so tests can shorten it.
var writeTimeout = 10 * time.Second

// ErrWriteBroken reports a connection whose cipher stream ran ahead of what the
// peer received. Nothing can be sent on it again.
var ErrWriteBroken = errors.New("kvm: connection write failed part way through")

func (i Info) networkAddress() string {
	return net.JoinHostPort(i.Host, strconv.Itoa(int(i.Port)))
}

func DeriveKeys(master []byte) (inboundKey, outboundKey []byte) {
	return DeriveKeyPair(master, 0)
}

func DeriveKeyPair(master []byte, skipPairs int) (inboundKey, outboundKey []byte) {
	kdf := NewKDF(master)
	if skipPairs > 0 {
		discard := make([]byte, skipPairs*32)
		kdf.Derive(discard)
	}
	inboundKey = make([]byte, 16)
	outboundKey = make([]byte, 16)
	kdf.Derive(inboundKey)
	kdf.Derive(outboundKey)
	return inboundKey, outboundKey
}

func DialWithKeys(ctx context.Context, info Info, inboundKey, outboundKey []byte) (*Conn, Status, error) {
	return dialWithKeys(ctx, info, inboundKey, outboundKey)
}

func dialWithKeys(ctx context.Context, info Info, inboundKey, outboundKey []byte) (*Conn, Status, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", info.networkAddress())
	if err != nil {
		return nil, StatusBadRequest, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := nc.SetDeadline(deadline); err != nil {
			_ = nc.Close()
			return nil, StatusBadRequest, err
		}
		defer nc.SetDeadline(time.Time{})
	}
	c := &Conn{net: nc}
	if info.ProtocolVersion <= 1 {
		status, err := c.negotiateLegacy(ctx, info)
		if err != nil {
			_ = nc.Close()
			return nil, StatusBadRequest, err
		}
		if status != StatusSuccess {
			_ = nc.Close()
			return nil, status, nil
		}
		return c, StatusSuccess, nil
	}
	enc, err := NewAESStream(inboundKey, nil)
	if err != nil {
		_ = nc.Close()
		return nil, StatusBadRequest, err
	}
	hello := NewClientHello(enc.IV(), info.Command, info.Channel, info.SessionKey)
	wireHello := hello.MarshalBinary()
	enc.XORKeyStream(wireHello[16:], wireHello[16:])
	if _, err := nc.Write(wireHello); err != nil {
		_ = nc.Close()
		return nil, StatusBadRequest, err
	}

	wireReply := make([]byte, serverHelloLen)
	if _, err := io.ReadFull(nc, wireReply); err != nil {
		_ = nc.Close()
		return nil, StatusBadRequest, err
	}
	dec, err := NewAESStream(outboundKey, wireReply[:16])
	if err != nil {
		_ = nc.Close()
		return nil, StatusBadRequest, err
	}
	dec.XORKeyStream(wireReply[16:], wireReply[16:])
	reply, err := UnmarshalServerHello(wireReply)
	if err != nil {
		_ = nc.Close()
		return nil, StatusBadRequest, err
	}
	c.encryptor = enc
	c.decryptor = dec
	if reply.Status != StatusSuccess {
		_ = nc.Close()
		return nil, reply.Status, nil
	}
	return c, StatusSuccess, nil
}

func (c *Conn) Close() error {
	if c == nil || c.net == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.legacyShared {
			_, _ = c.Write(LegacyShareClosingRecord())
		}
		c.closeErr = c.net.Close()
	})
	return c.closeErr
}

func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	n, err := c.net.Read(p)
	if n > 0 && c.decryptor != nil {
		c.decryptor.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.writeBroken {
		return 0, ErrWriteBroken
	}
	out := append([]byte(nil), p...)
	if c.encryptor != nil {
		c.encryptor.XORKeyStream(out, out)
	}
	// Without a deadline a half-open socket blocks the caller for as long as
	// the operating system keeps retransmitting, which is minutes.
	_ = c.net.SetWriteDeadline(time.Now().Add(writeTimeout))
	n, err := c.net.Write(out)
	_ = c.net.SetWriteDeadline(time.Time{})
	if err == nil && n != len(out) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// The cipher stream has already consumed key material for these bytes,
		// so a partial write leaves the two sides out of step for good.
		c.writeBroken = true
	}
	return n, err
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.net.SetReadDeadline(t)
}
