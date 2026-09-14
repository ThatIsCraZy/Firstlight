package rfb

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"io"
	"testing"
)

// pipeTransport is a Transport backed by two buffers, so a test can script
// the server side and inspect what the client wrote.
type pipeTransport struct {
	in  *bytes.Buffer
	out *bytes.Buffer
}

func newTransport(serverBytes []byte) *pipeTransport {
	return &pipeTransport{in: bytes.NewBuffer(serverBytes), out: &bytes.Buffer{}}
}

func (p *pipeTransport) Read(b []byte) (int, error) {
	if p.in.Len() == 0 {
		return 0, io.EOF
	}
	return p.in.Read(b)
}

func (p *pipeTransport) Write(b []byte) (int, error) { return p.out.Write(b) }

// serverInitBytes builds a ServerInit message with the 5/5/5 format the iDRAC
// reports.
func serverInitBytes(w, h int, name string) []byte {
	out := make([]byte, 24)
	binary.BigEndian.PutUint16(out[0:2], uint16(w))
	binary.BigEndian.PutUint16(out[2:4], uint16(h))
	out[4] = 16 // bits per pixel
	out[5] = 16 // depth
	out[6] = 0  // little endian
	out[7] = 1  // true colour
	binary.BigEndian.PutUint16(out[8:10], 31)
	binary.BigEndian.PutUint16(out[10:12], 31)
	binary.BigEndian.PutUint16(out[12:14], 31)
	out[14] = 10
	out[15] = 5
	out[16] = 0
	binary.BigEndian.PutUint32(out[20:24], uint32(len(name)))
	return append(out, name...)
}

func handshakeScript(w, h int, name string) []byte {
	script := []byte("RFB 003.008\n")
	script = append(script, 1, 1)       // one security type: None
	script = append(script, 0, 0, 0, 0) // SecurityResult: ok
	return append(script, serverInitBytes(w, h, name)...)
}

func TestHandshakeReadsServerInit(t *testing.T) {
	transport := newTransport(handshakeScript(1024, 768, "OpenVNC"))
	conn := New(transport)
	init, err := conn.Handshake(true)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if init.Width != 1024 || init.Height != 768 {
		t.Fatalf("size = %dx%d, want 1024x768", init.Width, init.Height)
	}
	if init.Name != "OpenVNC" {
		t.Fatalf("name = %q", init.Name)
	}
	if init.Format.BitsPerPixel != 16 || init.Format.RedShift != 10 {
		t.Fatalf("format = %+v", init.Format)
	}
	if bounds := conn.Screen().Bounds(); bounds.Dx() != 1024 || bounds.Dy() != 768 {
		t.Fatalf("screen bounds = %v", bounds)
	}
	written := transport.out.Bytes()
	if string(written[:12]) != "RFB 003.008\n" {
		t.Fatalf("client version = %q", written[:12])
	}
	if written[12] != 1 {
		t.Fatalf("security choice = %d, want 1 (None)", written[12])
	}
	if written[13] != 1 {
		t.Fatalf("ClientInit shared flag = %d, want 1", written[13])
	}
}

func TestHandshakeRejectsUnsupportedSecurity(t *testing.T) {
	script := []byte("RFB 003.008\n")
	script = append(script, 1, 2) // only VNC authentication
	conn := New(newTransport(script))
	if _, err := conn.Handshake(true); err == nil {
		t.Fatal("expected an error when only VNC authentication is offered")
	}
}

func TestHandshakeSurfacesServerFailureReason(t *testing.T) {
	script := []byte("RFB 003.008\n")
	script = append(script, 0)          // no security types
	script = append(script, 0, 0, 0, 4) // reason length
	script = append(script, []byte("busy")...)
	conn := New(newTransport(script))
	_, err := conn.Handshake(true)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); !bytes.Contains([]byte(got), []byte("busy")) {
		t.Fatalf("error = %q, want it to mention the server reason", got)
	}
}

// The client must never offer a version the server does not know.
func TestHandshakeClampsToServerVersion(t *testing.T) {
	script := []byte("RFB 003.007\n")
	script = append(script, 1, 1)
	script = append(script, 0, 0, 0, 0)
	script = append(script, serverInitBytes(640, 480, "x")...)
	transport := newTransport(script)
	conn := New(transport)
	if _, err := conn.Handshake(false); err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if got := string(transport.out.Bytes()[:12]); got != "RFB 003.007\n" {
		t.Fatalf("client version = %q, want RFB 003.007", got)
	}
}

// pixel555 packs a colour the way the iDRAC sends it.
func pixel555(r, g, b uint8) []byte {
	v := uint16(r>>3)<<10 | uint16(g>>3)<<5 | uint16(b>>3)
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, v)
	return out
}

func connectedConn(t *testing.T, extra []byte) (*Conn, *pipeTransport) {
	t.Helper()
	script := append(handshakeScript(4, 2, "test"), extra...)
	transport := newTransport(script)
	conn := New(transport)
	if _, err := conn.Handshake(true); err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	return conn, transport
}

func framebufferUpdate(rects ...[]byte) []byte {
	out := []byte{msgFramebufferUpdate, 0}
	var count [2]byte
	binary.BigEndian.PutUint16(count[:], uint16(len(rects)))
	out = append(out, count[:]...)
	for _, rect := range rects {
		out = append(out, rect...)
	}
	return out
}

func rectHeader(x, y, w, h int, encoding int32) []byte {
	out := make([]byte, 12)
	binary.BigEndian.PutUint16(out[0:2], uint16(x))
	binary.BigEndian.PutUint16(out[2:4], uint16(y))
	binary.BigEndian.PutUint16(out[4:6], uint16(w))
	binary.BigEndian.PutUint16(out[6:8], uint16(h))
	binary.BigEndian.PutUint32(out[8:12], uint32(encoding))
	return out
}

func TestDecodeRawPaintsPixels(t *testing.T) {
	body := rectHeader(0, 0, 2, 1, EncodingRaw)
	body = append(body, pixel555(255, 0, 0)...)
	body = append(body, pixel555(0, 0, 255)...)
	conn, _ := connectedConn(t, framebufferUpdate(body))

	update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if update == nil || update.Rectangles != 1 {
		t.Fatalf("update = %+v", update)
	}
	frame := conn.Screen().CopyFrame(nil)
	if got := frame.RGBAAt(0, 0); got.R < 240 || got.G != 0 || got.B != 0 {
		t.Fatalf("pixel(0,0) = %+v, want red", got)
	}
	if got := frame.RGBAAt(1, 0); got.B < 240 || got.R != 0 {
		t.Fatalf("pixel(1,0) = %+v, want blue", got)
	}
	if conn.Screen().Revision() != 1 {
		t.Fatalf("revision = %d, want 1", conn.Screen().Revision())
	}
}

func TestDecodeCopyRectMovesPixels(t *testing.T) {
	first := rectHeader(0, 0, 1, 1, EncodingRaw)
	first = append(first, pixel555(0, 255, 0)...)
	copyBody := rectHeader(2, 1, 1, 1, EncodingCopyRect)
	copyBody = append(copyBody, 0, 0, 0, 0) // source 0,0
	conn, _ := connectedConn(t, append(framebufferUpdate(first), framebufferUpdate(copyBody)...))

	if _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("first update: %v", err)
	}
	if _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("second update: %v", err)
	}
	frame := conn.Screen().CopyFrame(nil)
	if got := frame.RGBAAt(2, 1); got.G < 240 {
		t.Fatalf("copied pixel = %+v, want green", got)
	}
}

func TestDecodeHextileSolidTileAndSubrects(t *testing.T) {
	body := rectHeader(0, 0, 4, 2, EncodingHextile)
	// One tile: background specified, one coloured subrectangle.
	body = append(body, hextileBackgroundSpecified|hextileAnySubrects|hextileSubrectsColoured)
	body = append(body, pixel555(0, 0, 0)...)
	body = append(body, 1) // one subrectangle
	body = append(body, pixel555(255, 255, 255)...)
	body = append(body, 0x10, 0x00) // x=1 y=0, width 1 height 1
	conn, _ := connectedConn(t, framebufferUpdate(body))

	if _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	frame := conn.Screen().CopyFrame(nil)
	if got := frame.RGBAAt(0, 0); got.R != 0 || got.G != 0 || got.B != 0 {
		t.Fatalf("background pixel = %+v, want black", got)
	}
	if got := frame.RGBAAt(1, 0); got.R < 240 || got.G < 240 || got.B < 240 {
		t.Fatalf("subrect pixel = %+v, want white", got)
	}
}

func TestDecodeHextileRawTile(t *testing.T) {
	body := rectHeader(0, 0, 2, 1, EncodingHextile)
	body = append(body, hextileRaw)
	body = append(body, pixel555(255, 255, 0)...)
	body = append(body, pixel555(0, 255, 255)...)
	conn, _ := connectedConn(t, framebufferUpdate(body))

	if _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	frame := conn.Screen().CopyFrame(nil)
	if got := frame.RGBAAt(0, 0); got.R < 240 || got.G < 240 || got.B != 0 {
		t.Fatalf("pixel(0,0) = %+v, want yellow", got)
	}
}

func TestDecodeDesktopSizeResizes(t *testing.T) {
	body := rectHeader(0, 0, 800, 600, EncodingDesktop)
	conn, _ := connectedConn(t, framebufferUpdate(body))

	update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if !update.Resized {
		t.Fatal("update should report a resize")
	}
	if bounds := conn.Screen().Bounds(); bounds.Dx() != 800 || bounds.Dy() != 600 {
		t.Fatalf("bounds = %v, want 800x600", bounds)
	}
}

// LastRect ends an update whose rectangle count was only an upper bound.
func TestDecodeLastRectStopsEarly(t *testing.T) {
	out := []byte{msgFramebufferUpdate, 0, 0xff, 0xff}
	out = append(out, rectHeader(0, 0, 0, 0, EncodingLastRect)...)
	conn, _ := connectedConn(t, out)

	update, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if update.Rectangles != 1 {
		t.Fatalf("rectangles = %d, want 1", update.Rectangles)
	}
}

func TestReadMessageStoresServerCutText(t *testing.T) {
	body := []byte{msgServerCutText, 0, 0, 0, 0, 0, 0, 5}
	body = append(body, []byte("hello")...)
	conn, _ := connectedConn(t, body)

	if _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if got := conn.ServerCutText(); got != "hello" {
		t.Fatalf("clipboard = %q", got)
	}
}

func TestPointerAndKeyEventLayout(t *testing.T) {
	conn, transport := connectedConn(t, nil)
	transport.out.Reset()

	if err := conn.PointerEvent(1, 300, 200); err != nil {
		t.Fatalf("PointerEvent: %v", err)
	}
	if err := conn.KeyEvent(true, 0xff0d); err != nil {
		t.Fatalf("KeyEvent: %v", err)
	}
	written := transport.out.Bytes()
	if written[0] != msgPointerEvent || written[1] != 1 {
		t.Fatalf("pointer header = % x", written[:2])
	}
	if x := binary.BigEndian.Uint16(written[2:4]); x != 300 {
		t.Fatalf("pointer x = %d", x)
	}
	key := written[6:]
	if key[0] != msgKeyEvent || key[1] != 1 {
		t.Fatalf("key header = % x", key[:2])
	}
	if sym := binary.BigEndian.Uint32(key[4:8]); sym != 0xff0d {
		t.Fatalf("keysym = %#x", sym)
	}
}

func TestSetEncodingsLayout(t *testing.T) {
	conn, transport := connectedConn(t, nil)
	transport.out.Reset()
	if err := conn.SetEncodings([]int32{EncodingCopyRect, EncodingHextile, EncodingRaw, EncodingDesktop}); err != nil {
		t.Fatalf("SetEncodings: %v", err)
	}
	written := transport.out.Bytes()
	if written[0] != msgSetEncodings {
		t.Fatalf("message type = %d", written[0])
	}
	if n := binary.BigEndian.Uint16(written[2:4]); n != 4 {
		t.Fatalf("count = %d, want 4", n)
	}
	if got := int32(binary.BigEndian.Uint32(written[16:20])); got != EncodingDesktop {
		t.Fatalf("last encoding = %d, want %d", got, EncodingDesktop)
	}
}

func TestColorScalesChannelsToFullRange(t *testing.T) {
	if got := Depth16.Color(pixel555(255, 255, 255)); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("white = %+v", got)
	}
	if got := Depth16.Color(pixel555(0, 0, 0)); got != (color.RGBA{0, 0, 0, 255}) {
		t.Fatalf("black = %+v", got)
	}
}
