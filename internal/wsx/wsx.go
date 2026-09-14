// Package wsx implements the small part of RFC 6455 that a console client
// needs: a client-side handshake over TLS, binary data messages, and the
// control frames a server expects an endpoint to answer. It exists because
// Firstlight ships without third-party dependencies.
package wsx

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// magic is the GUID RFC 6455 appends to the client key before hashing.
const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xa
)

// MaxMessageSize caps a single reassembled message. A full 1600x1200 frame in
// 16-bit colour is under 4 MB, so 32 MB leaves ample headroom while still
// bounding what a hostile peer can make the client allocate.
const MaxMessageSize = 32 << 20

var (
	// ErrClosed reports that the peer sent a close frame.
	ErrClosed = errors.New("websocket closed by peer")
	// ErrMessageTooLarge reports a message beyond MaxMessageSize.
	ErrMessageTooLarge = errors.New("websocket message exceeds size limit")
)

// Options configures Dial.
type Options struct {
	// URL is the wss:// or https:// endpoint to upgrade.
	URL string
	// Subprotocol is offered in Sec-WebSocket-Protocol when not empty.
	Subprotocol string
	// Header carries additional request headers, for example Cookie.
	Header http.Header
	// TLSConfig overrides the default, which verifies certificates.
	TLSConfig *tls.Config
	// HandshakeTimeout bounds the connect and upgrade. Zero means 30 seconds.
	HandshakeTimeout time.Duration
}

// Conn is a client-side WebSocket connection. A Conn is safe for one reader
// and one writer used concurrently; it is not safe for two concurrent readers.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader

	writeMu sync.Mutex

	// subprotocol is what the server selected, empty when it selected none.
	subprotocol string

	closeOnce sync.Once
}

// Dial performs the TCP connect, TLS handshake and WebSocket upgrade.
func Dial(ctx context.Context, opts Options) (*Conn, error) {
	target, err := url.Parse(opts.URL)
	if err != nil {
		return nil, fmt.Errorf("websocket url: %w", err)
	}
	switch target.Scheme {
	case "wss", "https":
	default:
		return nil, fmt.Errorf("websocket url must use wss, got %q", target.Scheme)
	}
	host := target.Host
	if target.Port() == "" {
		host = net.JoinHostPort(target.Hostname(), "443")
	}
	timeout := opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", host)
	if err != nil {
		return nil, err
	}
	cfg := opts.TLSConfig
	if cfg == nil {
		cfg = &tls.Config{ServerName: target.Hostname()}
	} else if cfg.ServerName == "" && !cfg.InsecureSkipVerify {
		cfg = cfg.Clone()
		cfg.ServerName = target.Hostname()
	}
	tlsConn := tls.Client(raw, cfg)
	if err := tlsConn.HandshakeContext(dialCtx); err != nil {
		_ = raw.Close()
		return nil, err
	}

	// The deadline covers the upgrade exchange and is cleared afterwards so
	// long-lived reads are governed by the caller instead.
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = tlsConn.SetDeadline(deadline)
	}
	key, err := nonce()
	if err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	if err := writeUpgrade(tlsConn, target, host, key, opts); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(tlsConn, 32*1024)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = tlsConn.Close()
		return nil, fmt.Errorf("websocket upgrade: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = tlsConn.Close()
		return nil, fmt.Errorf("websocket upgrade rejected: HTTP %d %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != acceptKey(key) {
		_ = tlsConn.Close()
		return nil, errors.New("websocket upgrade returned a wrong accept key")
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return &Conn{
		conn:        tlsConn,
		br:          br,
		subprotocol: resp.Header.Get("Sec-WebSocket-Protocol"),
	}, nil
}

func writeUpgrade(w io.Writer, target *url.URL, host, key string, opts Options) error {
	path := target.RequestURI()
	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&req, "Host: %s\r\n", target.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", key)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	if opts.Subprotocol != "" {
		fmt.Fprintf(&req, "Sec-WebSocket-Protocol: %s\r\n", opts.Subprotocol)
	}
	for name, values := range opts.Header {
		for _, value := range values {
			fmt.Fprintf(&req, "%s: %s\r\n", name, value)
		}
	}
	req.WriteString("\r\n")
	_, err := io.WriteString(w, req.String())
	return err
}

func nonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw[:]), nil
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + magic)) //nolint:gosec // RFC 6455 mandates SHA-1 here.
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Subprotocol reports the subprotocol the server selected.
func (c *Conn) Subprotocol() string { return c.subprotocol }

// SetReadDeadline bounds the next ReadMessage.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// ReadMessage returns the payload of the next text or binary message.
// Fragmented messages are reassembled; ping frames are answered and close
// frames surface as ErrClosed.
func (c *Conn) ReadMessage() ([]byte, error) {
	var assembled []byte
	fragmented := false
	for {
		frame, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch frame.opcode {
		case opPing:
			if err := c.writeFrame(opPong, frame.payload); err != nil {
				return nil, err
			}
		case opPong:
			// Unsolicited pongs are legal and carry no meaning here.
		case opClose:
			_ = c.writeFrame(opClose, frame.payload)
			return nil, closeError(frame.payload)
		case opText, opBinary:
			if fragmented {
				return nil, errors.New("websocket: data frame inside a fragmented message")
			}
			if frame.fin {
				return frame.payload, nil
			}
			assembled = frame.payload
			fragmented = true
		case opContinuation:
			if !fragmented {
				return nil, errors.New("websocket: continuation without a start frame")
			}
			if len(assembled)+len(frame.payload) > MaxMessageSize {
				return nil, ErrMessageTooLarge
			}
			assembled = append(assembled, frame.payload...)
			if frame.fin {
				return assembled, nil
			}
		default:
			return nil, fmt.Errorf("websocket: unknown opcode %d", frame.opcode)
		}
	}
}

// WriteMessage sends one binary message.
func (c *Conn) WriteMessage(payload []byte) error {
	return c.writeFrame(opBinary, payload)
}

// Ping sends a ping frame with an empty payload.
func (c *Conn) Ping() error { return c.writeFrame(opPing, nil) }

// Close sends a close frame and tears the connection down.
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.conn.SetWriteDeadline(time.Now().Add(time.Second))
		_ = c.writeFrame(opClose, []byte{0x03, 0xe8}) // 1000, normal closure
		err = c.conn.Close()
	})
	return err
}

// newConn wraps an established byte stream. Dial uses it after the upgrade;
// tests use it to exercise the frame codec over net.Pipe.
func newConn(rw net.Conn) *Conn {
	return &Conn{conn: rw, br: bufio.NewReader(rw)}
}

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func (c *Conn) readFrame() (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.br, header[:]); err != nil {
		return frame{}, err
	}
	f := frame{
		fin:    header[0]&0x80 != 0,
		opcode: header[0] & 0x0f,
	}
	if header[0]&0x70 != 0 {
		return frame{}, errors.New("websocket: reserved bits set, no extension was negotiated")
	}
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return frame{}, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if f.opcode >= opClose {
		if !f.fin {
			return frame{}, errors.New("websocket: fragmented control frame")
		}
		if length > 125 {
			return frame{}, errors.New("websocket: oversized control frame")
		}
	}
	if length > MaxMessageSize {
		return frame{}, ErrMessageTooLarge
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return frame{}, err
		}
	}
	if length > 0 {
		f.payload = make([]byte, length)
		if _, err := io.ReadFull(c.br, f.payload); err != nil {
			return frame{}, err
		}
		if masked {
			for i := range f.payload {
				f.payload[i] ^= mask[i%4]
			}
		}
	}
	return f, nil
}

func (c *Conn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n <= 0xffff:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		header = append(header, ext[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	out := make([]byte, 0, len(header)+n)
	out = append(out, header...)
	for i := 0; i < n; i++ {
		out = append(out, payload[i]^mask[i%4])
	}
	_, err := c.conn.Write(out)
	return err
}

func closeError(payload []byte) error {
	if len(payload) < 2 {
		return ErrClosed
	}
	code := binary.BigEndian.Uint16(payload[:2])
	reason := strings.TrimSpace(string(payload[2:]))
	if reason == "" {
		return fmt.Errorf("%w (code %d)", ErrClosed, code)
	}
	return fmt.Errorf("%w (code %d: %s)", ErrClosed, code, reason)
}
