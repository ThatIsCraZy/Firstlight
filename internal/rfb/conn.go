// Package rfb implements the client half of the Remote Framebuffer protocol
// (RFC 6143) that Dell's iDRAC serves for its HTML5 virtual console. It knows
// nothing about the vendor: a caller supplies a byte-stream transport and gets
// a framebuffer plus keyboard and pointer input in return.
package rfb

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Encoding numbers used by this client. Tight and its friends are left out
// because the iDRAC negotiates 16-bit colour, where noVNC itself asks for
// nothing beyond CopyRect, Hextile and Raw.
const (
	EncodingRaw       int32 = 0
	EncodingCopyRect  int32 = 1
	EncodingRRE       int32 = 2
	EncodingHextile   int32 = 5
	EncodingCursor    int32 = -239
	EncodingDesktop   int32 = -223
	EncodingLastRect  int32 = -224
	EncodingQEMUExtKB int32 = -258
)

// Server-to-client message types.
const (
	msgFramebufferUpdate   = 0
	msgSetColourMapEntries = 1
	msgBell                = 2
	msgServerCutText       = 3
)

// Client-to-server message types.
const (
	msgSetPixelFormat  = 0
	msgSetEncodings    = 2
	msgUpdateRequest   = 3
	msgKeyEvent        = 4
	msgPointerEvent    = 5
	msgClientCutText   = 6
	msgVendorPassthru  = 247 // Dell multiplexes its own control messages here.
	maxCutTextLength   = 1 << 20
	maxRectanglesPerFB = 4096
)

// Transport carries the raw RFB byte stream. The iDRAC backend supplies one
// that has already filtered out Dell's own control frames.
type Transport interface {
	io.Reader
	io.Writer
}

// ServerInit is what the server reports once the handshake completes.
type ServerInit struct {
	Width  int
	Height int
	Format PixelFormat
	Name   string
}

// Update describes one decoded framebuffer update.
type Update struct {
	Rectangles int
	Resized    bool
}

// Conn is a client-side RFB connection.
type Conn struct {
	transport Transport
	reader    *bufio.Reader
	screen    *Screen

	writeMu sync.Mutex

	format PixelFormat
	init   ServerInit

	// cutText holds the most recent clipboard text the server offered.
	cutMu   sync.Mutex
	cutText string

	// logf receives decoder diagnostics when a caller asks for them.
	logf func(format string, args ...any)
}

// SetLogger installs a diagnostic sink. It is meant for tracking down
// rendering faults and stays off unless a caller sets it.
func (c *Conn) SetLogger(logf func(format string, args ...any)) {
	c.logf = logf
}

func (c *Conn) log(format string, args ...any) {
	if c.logf != nil {
		c.logf(format, args...)
	}
}

// New wraps a transport. Handshake must run before anything else.
func New(transport Transport) *Conn {
	return &Conn{
		transport: transport,
		reader:    bufio.NewReaderSize(transport, 64*1024),
		screen:    NewScreen(1, 1),
	}
}

// Screen exposes the framebuffer.
func (c *Conn) Screen() *Screen { return c.screen }

// ServerInit reports the values received during the handshake.
func (c *Conn) ServerInit() ServerInit { return c.init }

// Format reports the pixel format currently in force.
func (c *Conn) Format() PixelFormat { return c.format }

// Handshake runs the version, security and initialisation exchange. Passing
// shared keeps other clients connected instead of displacing them.
func (c *Conn) Handshake(shared bool) (ServerInit, error) {
	version, err := c.read(12)
	if err != nil {
		return ServerInit{}, fmt.Errorf("read server version: %w", err)
	}
	major, minor, err := parseVersion(version)
	if err != nil {
		return ServerInit{}, err
	}
	// Never offer more than the server does, and never more than 3.8.
	if major > 3 || (major == 3 && minor > 8) {
		major, minor = 3, 8
	}
	if _, err := c.write([]byte(fmt.Sprintf("RFB %03d.%03d\n", major, minor))); err != nil {
		return ServerInit{}, err
	}
	if err := c.negotiateSecurity(major, minor); err != nil {
		return ServerInit{}, err
	}
	clientInit := byte(0)
	if shared {
		clientInit = 1
	}
	if _, err := c.write([]byte{clientInit}); err != nil {
		return ServerInit{}, err
	}
	body, err := c.read(24)
	if err != nil {
		return ServerInit{}, fmt.Errorf("read ServerInit: %w", err)
	}
	init := ServerInit{
		Width:  int(binary.BigEndian.Uint16(body[0:2])),
		Height: int(binary.BigEndian.Uint16(body[2:4])),
		Format: PixelFormat{}.parse(body[4:20]),
	}
	nameLen := binary.BigEndian.Uint32(body[20:24])
	if nameLen > 4096 {
		return ServerInit{}, fmt.Errorf("ServerInit name length %d is implausible", nameLen)
	}
	name, err := c.read(int(nameLen))
	if err != nil {
		return ServerInit{}, err
	}
	init.Name = string(name)
	c.init = init
	c.format = init.Format
	c.screen.Resize(init.Width, init.Height)
	return init, nil
}

func parseVersion(raw []byte) (major, minor int, err error) {
	if len(raw) != 12 || string(raw[:4]) != "RFB " || raw[7] != '.' || raw[11] != '\n' {
		return 0, 0, fmt.Errorf("malformed RFB version %q", raw)
	}
	if _, err := fmt.Sscanf(string(raw[4:11]), "%3d.%3d", &major, &minor); err != nil {
		return 0, 0, fmt.Errorf("malformed RFB version %q", raw)
	}
	if major != 3 {
		return 0, 0, fmt.Errorf("unsupported RFB major version %d", major)
	}
	return major, minor, nil
}

func (c *Conn) negotiateSecurity(major, minor int) error {
	if major == 3 && minor < 7 {
		// Version 3.3 has the server pick, and it sends the choice as uint32.
		raw, err := c.read(4)
		if err != nil {
			return err
		}
		switch binary.BigEndian.Uint32(raw) {
		case 1:
			return c.readSecurityResult(major, minor)
		case 0:
			return c.readFailureReason("connection refused")
		default:
			return errors.New("server demands an authentication scheme this client does not implement")
		}
	}
	count, err := c.read(1)
	if err != nil {
		return err
	}
	if count[0] == 0 {
		return c.readFailureReason("no security type offered")
	}
	types, err := c.read(int(count[0]))
	if err != nil {
		return err
	}
	// Type 1 is None. The iDRAC authenticates through its console ticket
	// before RFB starts, so None is what it offers.
	for _, t := range types {
		if t == 1 {
			if _, err := c.write([]byte{1}); err != nil {
				return err
			}
			return c.readSecurityResult(major, minor)
		}
	}
	return fmt.Errorf("server offers security types %v, none of which this client implements", types)
}

func (c *Conn) readSecurityResult(major, minor int) error {
	result, err := c.read(4)
	if err != nil {
		return err
	}
	if binary.BigEndian.Uint32(result) == 0 {
		return nil
	}
	if major == 3 && minor >= 8 {
		return c.readFailureReason("authentication failed")
	}
	return errors.New("authentication failed")
}

func (c *Conn) readFailureReason(fallback string) error {
	length, err := c.read(4)
	if err != nil {
		return errors.New(fallback)
	}
	n := binary.BigEndian.Uint32(length)
	if n == 0 || n > 4096 {
		return errors.New(fallback)
	}
	reason, err := c.read(int(n))
	if err != nil {
		return errors.New(fallback)
	}
	return fmt.Errorf("%s: %s", fallback, reason)
}

// SetPixelFormat asks the server to deliver pixels in the given format.
func (c *Conn) SetPixelFormat(format PixelFormat) error {
	message := append([]byte{msgSetPixelFormat, 0, 0, 0}, format.encode()...)
	if _, err := c.write(message); err != nil {
		return err
	}
	c.format = format
	return nil
}

// SetEncodings declares which encodings the client understands, most
// preferred first.
func (c *Conn) SetEncodings(encodings []int32) error {
	message := make([]byte, 4, 4+4*len(encodings))
	message[0] = msgSetEncodings
	binary.BigEndian.PutUint16(message[2:4], uint16(len(encodings)))
	for _, encoding := range encodings {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(encoding))
		message = append(message, raw[:]...)
	}
	_, err := c.write(message)
	return err
}

// RequestUpdate asks for a framebuffer update. An incremental request returns
// only what changed; a full one repaints the region.
func (c *Conn) RequestUpdate(incremental bool, x, y, w, h int) error {
	message := make([]byte, 10)
	message[0] = msgUpdateRequest
	if incremental {
		message[1] = 1
	}
	binary.BigEndian.PutUint16(message[2:4], uint16(x))
	binary.BigEndian.PutUint16(message[4:6], uint16(y))
	binary.BigEndian.PutUint16(message[6:8], uint16(w))
	binary.BigEndian.PutUint16(message[8:10], uint16(h))
	_, err := c.write(message)
	return err
}

// RequestFullUpdate repaints the whole framebuffer.
func (c *Conn) RequestFullUpdate() error {
	bounds := c.screen.Bounds()
	return c.RequestUpdate(false, 0, 0, bounds.Dx(), bounds.Dy())
}

// KeyEvent presses or releases one X11 keysym.
func (c *Conn) KeyEvent(down bool, keysym uint32) error {
	message := make([]byte, 8)
	message[0] = msgKeyEvent
	if down {
		message[1] = 1
	}
	binary.BigEndian.PutUint32(message[4:8], keysym)
	_, err := c.write(message)
	return err
}

// PointerEvent reports the pointer position and the current button mask.
// Bit 0 is left, bit 1 middle, bit 2 right, bits 3 and 4 are wheel up and down.
func (c *Conn) PointerEvent(buttons byte, x, y int) error {
	message := make([]byte, 6)
	message[0] = msgPointerEvent
	message[1] = buttons
	binary.BigEndian.PutUint16(message[2:4], uint16(clampUint16(x)))
	binary.BigEndian.PutUint16(message[4:6], uint16(clampUint16(y)))
	_, err := c.write(message)
	return err
}

// ClientCutText offers clipboard text to the server. Few BMC firmwares act on
// it, which is why Firstlight also types the clipboard as key events.
func (c *Conn) ClientCutText(text string) error {
	if len(text) > maxCutTextLength {
		return errors.New("clipboard text is too long")
	}
	message := make([]byte, 8, 8+len(text))
	message[0] = msgClientCutText
	binary.BigEndian.PutUint32(message[4:8], uint32(len(text)))
	message = append(message, text...)
	_, err := c.write(message)
	return err
}

// ServerCutText returns the most recent clipboard text the server sent.
func (c *Conn) ServerCutText() string {
	c.cutMu.Lock()
	defer c.cutMu.Unlock()
	return c.cutText
}

// VendorMessage sends a raw type-247 message. Dell uses this range for power
// control, boot device selection and session sharing.
func (c *Conn) VendorMessage(payload []byte) error {
	_, err := c.write(payload)
	return err
}

// ReadMessage reads and applies exactly one server message. A framebuffer
// update returns a non-nil Update.
func (c *Conn) ReadMessage() (*Update, error) {
	kind, err := c.read(1)
	if err != nil {
		return nil, err
	}
	switch kind[0] {
	case msgFramebufferUpdate:
		return c.readFramebufferUpdate()
	case msgSetColourMapEntries:
		return nil, c.skipColourMap()
	case msgBell:
		return nil, nil
	case msgServerCutText:
		return nil, c.readServerCutText()
	default:
		return nil, fmt.Errorf("unknown RFB server message type %d", kind[0])
	}
}

func (c *Conn) skipColourMap() error {
	header, err := c.read(5)
	if err != nil {
		return err
	}
	count := int(binary.BigEndian.Uint16(header[3:5]))
	_, err = c.read(count * 6)
	return err
}

func (c *Conn) readServerCutText() error {
	header, err := c.read(7)
	if err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[3:7])
	if length > maxCutTextLength {
		return fmt.Errorf("server clipboard text of %d bytes is too large", length)
	}
	text, err := c.read(int(length))
	if err != nil {
		return err
	}
	c.cutMu.Lock()
	c.cutText = string(text)
	c.cutMu.Unlock()
	return nil
}

func (c *Conn) read(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("negative read length")
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *Conn) write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transport.Write(p)
}

func clampUint16(v int) int {
	if v < 0 {
		return 0
	}
	if v > 0xffff {
		return 0xffff
	}
	return v
}
