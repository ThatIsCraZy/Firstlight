package kvm

import (
	"context"
	"crypto/cipher"
	"crypto/rc4" //nolint:gosec // Required for the negotiated legacy iLO 4 wire protocol.
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

type LegacyCipher uint8

const (
	LegacyCipherNone LegacyCipher = iota
	LegacyCipherRC4
	LegacyCipherAES128
	LegacyCipherAES256
)

const (
	legacyMarker                    = 0x50
	legacyResponseDenied            = 0x51
	legacyResponseSuccess           = 0x52
	legacyResponseBusy              = 0x53
	legacyResponseSuccessUnlicensed = 0x57
	legacyResponseNoSessions        = 0x58
	legacyResponseBusyUnlicensed    = 0x59

	legacyKVMCommand     = 0x2001
	legacyCommandCommand = 0x2002
	legacyAcquireCommand = 0x0055
	legacyShareCommand   = 0x0056
	legacyEncryptedKey   = 0x8000
	legacyEncryptedVMKey = 0x4000
)

var legacyShareAccept = acceptLegacySharedPeer

func (c *Conn) negotiateLegacy(ctx context.Context, info Info) (Status, error) {
	if info.Legacy == nil {
		return StatusBadRequest, fmt.Errorf("legacy protocol v%d requires legacy RcInfo data", info.ProtocolVersion)
	}
	baseCommand, err := waitForLegacyMarker(c.net, info.Channel)
	if err != nil {
		return StatusBadRequest, err
	}
	request, err := marshalLegacyRequest(baseCommand, info.SessionKey, info.Legacy)
	if err != nil {
		return StatusBadRequest, err
	}
	if _, err := c.net.Write(request); err != nil {
		return StatusBadRequest, err
	}
	status, err := readLegacyStatus(c.net)
	if err != nil {
		return StatusBadRequest, err
	}
	if status == StatusBusy {
		switch info.Command {
		case CommandAcquire:
			if err := writeLegacyCommand(c.net, legacyAcquireCommand); err != nil {
				return StatusBadRequest, err
			}
			status, err = readLegacyStatus(c.net)
			if err != nil {
				return StatusBadRequest, err
			}
		case CommandShare:
			if err := writeLegacyCommand(c.net, legacyShareCommand); err != nil {
				return StatusBadRequest, err
			}
			status, err = readLegacyStatus(c.net)
			if err != nil {
				return StatusBadRequest, err
			}
			if status != StatusSuccess {
				return status, nil
			}
			_ = c.net.Close()
			peer, err := legacyShareAccept(ctx, info.Port, 10*time.Second)
			if err != nil {
				return StatusBadRequest, fmt.Errorf("legacy shared-session reverse listener: %w", err)
			}
			c.net = peer
			c.legacyKVM = true
			c.legacyKey = append([]byte(nil), info.Legacy.EncryptionKey...)
			c.legacyShared = true
			if _, err := c.Write(LegacyShareOpeningRecord()); err != nil {
				_ = peer.Close()
				return StatusBadRequest, fmt.Errorf("legacy shared-session opening: %w", err)
			}
			return StatusSuccess, nil
		}
	}
	if status != StatusSuccess {
		return status, nil
	}

	switch info.Channel {
	case ChannelKVM:
		if len(info.Legacy.EncryptionKey) == 0 {
			return StatusBadRequest, fmt.Errorf("legacy KVM encryption key is empty")
		}
		c.legacyKVM = true
		c.legacyKey = append([]byte(nil), info.Legacy.EncryptionKey...)
	case ChannelCmd:
		if info.Legacy.EncryptCommand {
			if _, err := c.net.Write([]byte{8, 0, 0, 0}); err != nil {
				return StatusBadRequest, err
			}
			encryptor, decryptor, err := newLegacyStreamPair(LegacyCipherAES128, info.Legacy.CommandKey)
			if err != nil {
				return StatusBadRequest, fmt.Errorf("legacy command encryption: %w", err)
			}
			c.encryptor = encryptor
			c.decryptor = decryptor
		}
	}
	return StatusSuccess, nil
}

func acceptLegacySharedPeer(ctx context.Context, port uint16, timeout time.Duration) (net.Conn, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	listenCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lc := net.ListenConfig{}
	listener, err := lc.Listen(listenCtx, "tcp", net.JoinHostPort("", strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	return acceptLegacySharedPeerFromListener(listenCtx, listener)
}

func acceptLegacySharedPeerFromListener(ctx context.Context, listener net.Listener) (net.Conn, error) {
	type acceptResult struct {
		conn net.Conn
		err  error
	}
	result := make(chan acceptResult, 1)
	go func() {
		conn, err := listener.Accept()
		result <- acceptResult{conn: conn, err: err}
	}()
	select {
	case accepted := <-result:
		if tcp, ok := accepted.conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		return accepted.conn, accepted.err
	case <-ctx.Done():
		_ = listener.Close()
		accepted := <-result
		if accepted.conn != nil {
			_ = accepted.conn.Close()
		}
		return nil, ctx.Err()
	}
}

func waitForLegacyMarker(r io.Reader, channel Channel) (uint16, error) {
	var command uint16
	switch channel {
	case ChannelKVM:
		command = legacyKVMCommand
		var marker [1]byte
		if _, err := io.ReadFull(r, marker[:]); err != nil {
			return 0, err
		}
		if marker[0] != legacyMarker {
			return 0, fmt.Errorf("legacy KVM marker is 0x%02x, want 0x%02x", marker[0], legacyMarker)
		}
	case ChannelCmd:
		command = legacyCommandCommand
		var marker [1]byte
		for i := 0; i < 30; i++ {
			if _, err := io.ReadFull(r, marker[:]); err != nil {
				return 0, err
			}
			if marker[0] == legacyMarker {
				return command, nil
			}
		}
		return 0, fmt.Errorf("legacy command marker 0x%02x not found in first 30 bytes", legacyMarker)
	default:
		return 0, fmt.Errorf("legacy negotiation does not support channel %d", channel)
	}
	return command, nil
}

func marshalLegacyRequest(command uint16, sessionKey string, options *LegacyOptions) ([]byte, error) {
	key := []byte(sessionKey)
	if len(key) > 32 {
		return nil, fmt.Errorf("legacy session key must be at most 32 bytes, got %d", len(key))
	}
	var field [32]byte
	copy(field[:], key)
	if options.EncryptSessionKey {
		mask := []byte(options.EncryptionKeyText)
		if len(mask) == 0 {
			return nil, fmt.Errorf("legacy ENCRYPT_KEY requires the original enc_key text")
		}
		for i := range key {
			field[i] ^= mask[i%len(mask)]
		}
		if options.EncryptVMKey {
			command |= legacyEncryptedVMKey
		} else {
			command |= legacyEncryptedKey
		}
	}
	out := make([]byte, 34)
	binary.LittleEndian.PutUint16(out[:2], command)
	copy(out[2:], field[:])
	return out, nil
}

func readLegacyStatus(r io.Reader) (Status, error) {
	var response [1]byte
	if _, err := io.ReadFull(r, response[:]); err != nil {
		return StatusBadRequest, err
	}
	switch response[0] {
	case legacyResponseSuccess, legacyResponseSuccessUnlicensed:
		return StatusSuccess, nil
	case legacyResponseBusy, legacyResponseBusyUnlicensed:
		return StatusBusy, nil
	case legacyResponseDenied:
		return StatusDenied, nil
	case legacyResponseNoSessions:
		return StatusNoSessions, nil
	default:
		return StatusBadRequest, fmt.Errorf("unknown legacy iLO response 0x%02x", response[0])
	}
}

func writeLegacyCommand(w io.Writer, command uint16) error {
	var request [2]byte
	binary.LittleEndian.PutUint16(request[:], command)
	_, err := w.Write(request[:])
	return err
}

func (c *Conn) SetLegacyKVMEncryption(mode LegacyCipher) error {
	if c == nil || !c.legacyKVM {
		return fmt.Errorf("connection is not a legacy KVM connection")
	}
	encryptor, decryptor, err := newLegacyStreamPair(mode, c.legacyKey)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	c.readMu.Lock()
	c.encryptor = encryptor
	c.decryptor = decryptor
	c.legacyCipher = mode
	c.readMu.Unlock()
	c.writeMu.Unlock()
	return nil
}

func newLegacyStreamPair(mode LegacyCipher, key []byte) (cipher.Stream, cipher.Stream, error) {
	switch mode {
	case LegacyCipherNone:
		return nil, nil, nil
	case LegacyCipherRC4:
		tx, err := rc4.NewCipher(key)
		if err != nil {
			return nil, nil, err
		}
		rx, err := rc4.NewCipher(key)
		if err != nil {
			return nil, nil, err
		}
		return tx, rx, nil
	case LegacyCipherAES128, LegacyCipherAES256:
		zeroIV := make([]byte, 16)
		tx, err := NewAESStream(key, zeroIV)
		if err != nil {
			return nil, nil, err
		}
		rx, err := NewAESStream(key, zeroIV)
		if err != nil {
			return nil, nil, err
		}
		return tx, rx, nil
	default:
		return nil, nil, fmt.Errorf("unsupported legacy KVM cipher %d", mode)
	}
}
