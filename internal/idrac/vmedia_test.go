package idrac

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"firstlight/internal/vmedia"
)

func TestWordsEncodesLittleEndian(t *testing.T) {
	got := words(0x2000, 1, 0x20, 0xdeadbeef)
	if len(got) != 16 {
		t.Fatalf("length = %d, want 16", len(got))
	}
	if binary.LittleEndian.Uint32(got[0:4]) != 0x2000 {
		t.Fatalf("first word = % x", got[0:4])
	}
	if binary.LittleEndian.Uint32(got[12:16]) != 0xdeadbeef {
		t.Fatalf("fourth word = % x", got[12:16])
	}
}

func TestPackWordFoldsFourCharacters(t *testing.T) {
	// "0123" little-endian is 0x33323130.
	if got := packWord("0123", 0); got != 0x33323130 {
		t.Fatalf("packWord = %#x", got)
	}
	// A short key pads with zeros rather than reading past the end.
	if got := packWord("ab", 0); got != 0x00006261 {
		t.Fatalf("short key = %#x", got)
	}
	if got := packWord("abcd", 8); got != 0 {
		t.Fatalf("offset past the end = %#x", got)
	}
}

func TestAsciiWordsStopsAtNul(t *testing.T) {
	raw := append([]byte("012345678901234567890123"), 0, 0, 0, 0)
	if got := asciiWords(raw); got != "012345678901234567890123" {
		t.Fatalf("asciiWords = %q", got)
	}
	if got := asciiWords([]byte{0, 'x'}); got != "" {
		t.Fatalf("leading zero should end the string, got %q", got)
	}
}

// testISO writes an image of the requested size and opens it.
func testISO(t *testing.T, size int) *vmedia.ISO {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.iso")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	iso, err := vmedia.OpenISO(path)
	if err != nil {
		t.Fatalf("OpenISO: %v", err)
	}
	t.Cleanup(func() { _ = iso.Close() })
	return iso
}

func mediaFixture(t *testing.T, size int) (*MediaSession, *scriptedWS) {
	t.Helper()
	socket := &scriptedWS{}
	iso := testISO(t, size)
	return &MediaSession{
		socket: socket,
		iso:    iso,
		done:   make(chan struct{}),
		blocks: uint32((iso.Size() + cdBlockSize - 1) / cdBlockSize),
		key2:   "012345678901234567890123",
	}, socket
}

func TestSendAuthLayout(t *testing.T) {
	media, socket := mediaFixture(t, cdBlockSize)
	if err := media.sendAuth(4711); err != nil {
		t.Fatalf("sendAuth: %v", err)
	}
	if len(socket.written) != 1 {
		t.Fatalf("messages = %d, want one", len(socket.written))
	}
	message := socket.written[0]
	if len(message) != 48 {
		t.Fatalf("length = %d, want 48", len(message))
	}
	if binary.LittleEndian.Uint32(message[0:4]) != mediaMsgAuthKey2 {
		t.Fatalf("type = %#x", binary.LittleEndian.Uint32(message[0:4]))
	}
	if binary.LittleEndian.Uint32(message[8:12]) != 0x20 {
		t.Fatalf("payload length = %d, want 32", binary.LittleEndian.Uint32(message[8:12]))
	}
	if binary.LittleEndian.Uint32(message[12:16]) != 4711 {
		t.Fatalf("token = %d", binary.LittleEndian.Uint32(message[12:16]))
	}
	// The key must come back out of the six words unchanged.
	if got := asciiWords(message[16:40]); got != "012345678901234567890123" {
		t.Fatalf("key = %q", got)
	}
}

func TestAnnounceMediumSendsInquiryAndFirstBlocks(t *testing.T) {
	media, socket := mediaFixture(t, 64*cdBlockSize)
	media.vmToken = 9
	if err := media.announceMedium(); err != nil {
		t.Fatalf("announceMedium: %v", err)
	}
	if len(socket.written) != 3 {
		t.Fatalf("messages = %d, want inquiry, read header and data", len(socket.written))
	}
	inquiry := socket.written[0]
	if len(inquiry) != 44 {
		t.Fatalf("inquiry length = %d, want 44", len(inquiry))
	}
	if binary.LittleEndian.Uint32(inquiry[0:4]) != mediaMsgInquiry {
		t.Fatalf("inquiry type = %#x", binary.LittleEndian.Uint32(inquiry[0:4]))
	}
	if got := binary.LittleEndian.Uint32(inquiry[16:20]); got != 64 {
		t.Fatalf("total blocks = %d, want 64", got)
	}
	if got := binary.LittleEndian.Uint32(inquiry[20:24]); got != cdBlockSize {
		t.Fatalf("block size = %d", got)
	}
	if got := binary.LittleEndian.Uint32(inquiry[36:40]); got != 1 {
		t.Fatalf("read-only flag = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(inquiry[40:44]); got != tocDataLen {
		t.Fatalf("toc length = %d", got)
	}

	header := socket.written[1]
	if binary.LittleEndian.Uint32(header[0:4]) != mediaMsgReadData {
		t.Fatalf("read header type = %#x", binary.LittleEndian.Uint32(header[0:4]))
	}
	if got := binary.LittleEndian.Uint32(header[20:24]); got != initialBlocks {
		t.Fatalf("initial blocks = %d, want %d", got, initialBlocks)
	}
	if got := len(socket.written[2]); got != initialBlocks*cdBlockSize {
		t.Fatalf("payload = %d bytes, want %d", got, initialBlocks*cdBlockSize)
	}
}

// An image shorter than the priming read must not ask for blocks that do not
// exist.
func TestAnnounceMediumClampsSmallImage(t *testing.T) {
	media, socket := mediaFixture(t, 4*cdBlockSize)
	if err := media.announceMedium(); err != nil {
		t.Fatalf("announceMedium: %v", err)
	}
	header := socket.written[1]
	if got := binary.LittleEndian.Uint32(header[20:24]); got != 4 {
		t.Fatalf("blocks = %d, want 4", got)
	}
	if got := len(socket.written[2]); got != 4*cdBlockSize {
		t.Fatalf("payload = %d bytes", got)
	}
}

func readRequest(startBlock, numBlocks, blockSize uint32) []byte {
	return words(mediaMsgReadData, mediaImageCD, 12, 1, startBlock, numBlocks, blockSize)
}

func TestServeReadReturnsTheRequestedBlocks(t *testing.T) {
	media, socket := mediaFixture(t, 64*cdBlockSize)
	if err := media.serveRead(readRequest(2, 3, cdBlockSize)); err != nil {
		t.Fatalf("serveRead: %v", err)
	}
	header := socket.written[0]
	if got := binary.LittleEndian.Uint32(header[16:20]); got != 2 {
		t.Fatalf("start block = %d", got)
	}
	if got := binary.LittleEndian.Uint32(header[24:28]); got != 5 {
		t.Fatalf("end block = %d, want 5", got)
	}
	if got := binary.LittleEndian.Uint32(header[32:36]); got != 3*cdBlockSize {
		t.Fatalf("data length = %d", got)
	}
	data := socket.written[1]
	if len(data) != 3*cdBlockSize {
		t.Fatalf("payload = %d bytes", len(data))
	}
	// The first payload byte must be the image byte at block two.
	if want := byte((2 * cdBlockSize) % 256); data[0] != want {
		t.Fatalf("first byte = %d, want %d", data[0], want)
	}
	if health := media.Health(); health.DeliveredBytes != uint64(3*cdBlockSize) {
		t.Fatalf("delivered = %d", health.DeliveredBytes)
	}
}

// The firmware probes past the end while it works out the geometry, so a read
// beyond the last block is trimmed rather than refused.
func TestServeReadClampsPastTheEnd(t *testing.T) {
	media, socket := mediaFixture(t, 4*cdBlockSize)
	if err := media.serveRead(readRequest(3, 8, cdBlockSize)); err != nil {
		t.Fatalf("serveRead: %v", err)
	}
	header := socket.written[0]
	if got := binary.LittleEndian.Uint32(header[20:24]); got != 1 {
		t.Fatalf("blocks = %d, want 1", got)
	}
	if got := len(socket.written[1]); got != cdBlockSize {
		t.Fatalf("payload = %d bytes", got)
	}
}

func TestServeReadRejectsAbsurdRequest(t *testing.T) {
	media, _ := mediaFixture(t, cdBlockSize)
	if err := media.serveRead(readRequest(0, maxBlocksPerRead+1, cdBlockSize)); err == nil {
		t.Fatal("expected an error for an oversized request")
	}
}

func TestServeReadRejectsShortMessage(t *testing.T) {
	media, _ := mediaFixture(t, cdBlockSize)
	if err := media.serveRead(words(mediaMsgReadData, 1, 12, 1)); err == nil {
		t.Fatal("expected an error for a truncated read request")
	}
}

func TestHandleConnectedAnnouncesOnce(t *testing.T) {
	media, socket := mediaFixture(t, 8*cdBlockSize)
	connected := append(words(mediaMsgConnected, 0, 0, 77), []byte("abcdefghijklmnopqrstuvwx\x00\x00\x00\x00")...)
	if err := media.handle(connected); err != nil {
		t.Fatalf("handle: %v", err)
	}
	first := len(socket.written)
	if first != 3 {
		t.Fatalf("messages = %d, want three", first)
	}
	if media.vmToken != 77 {
		t.Fatalf("token = %d", media.vmToken)
	}
	if media.key2 != "abcdefghijklmnopqrstuvwx" {
		t.Fatalf("refreshed key = %q", media.key2)
	}
	// A second ready message must not push the image again.
	if err := media.handle(connected); err != nil {
		t.Fatalf("second handle: %v", err)
	}
	if len(socket.written) != first {
		t.Fatalf("messages = %d, want no further traffic", len(socket.written))
	}
}

func TestHandleStatusMarksMappedAndReportsErrors(t *testing.T) {
	media, _ := mediaFixture(t, cdBlockSize)
	mapped := words(mediaMsgStatus, mediaImageCD, 0, 1, mediaStatusMapped)
	if err := media.handle(mapped); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if !media.Health().DeviceReady {
		t.Fatal("a mapped status should mark the device ready")
	}
	if got := media.LastStatus(); got != "medium mapped" {
		t.Fatalf("status text = %q", got)
	}
	disabled := words(mediaMsgStatus, mediaImageCD, 0, 1, mediaStatusDisabled)
	if err := media.handle(disabled); err == nil {
		t.Fatal("a disabled status should end the session")
	}
}

func TestHandleEjectAnswersAndStops(t *testing.T) {
	media, socket := mediaFixture(t, cdBlockSize)
	media.mapped = true
	err := media.handle(words(mediaMsgEject, mediaImageCD, 0, 1))
	if err == nil {
		t.Fatal("an eject should end the session")
	}
	if len(socket.written) != 1 {
		t.Fatalf("messages = %d, want the unmap answer", len(socket.written))
	}
	if binary.LittleEndian.Uint32(socket.written[0][0:4]) != mediaMsgEject {
		t.Fatalf("answer type = %#x", binary.LittleEndian.Uint32(socket.written[0][0:4]))
	}
	if media.Health().DeviceReady {
		t.Fatal("the device must not stay ready after an eject")
	}
}

func TestHandleAuthChallengeAnswersWithTheKey(t *testing.T) {
	media, socket := mediaFixture(t, cdBlockSize)
	if err := media.handle(words(mediaMsgAuthKey2, 0, 0, 0)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(socket.written) != 1 || binary.LittleEndian.Uint32(socket.written[0][0:4]) != mediaMsgAuthKey2 {
		t.Fatalf("written = %v", socket.written)
	}
}

func TestHandleRejectsShortMessage(t *testing.T) {
	media, _ := mediaFixture(t, cdBlockSize)
	if err := media.handle([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected an error for a short message")
	}
}

func TestMediaStatusTextCoversTheCommonCodes(t *testing.T) {
	if mediaStatusText(mediaStatusLicense) == "" {
		t.Fatal("licence errors need a message")
	}
	if got := mediaStatusText(9999); got == "" {
		t.Fatal("unknown codes still need a message")
	}
}

func TestMediaReadLoopReleasesTheTransport(t *testing.T) {
	media, socket := mediaFixture(t, cdBlockSize)
	media.ctx, media.cancel = context.WithCancel(context.Background())
	media.mapped = true
	// An eject from the host is fatal: handle answers it and then ends the
	// loop. That used to return without closing the WebSocket.
	socket.messages = [][]byte{words(mediaMsgEject, mediaImageCD, 0, 1)}

	media.readLoop()

	select {
	case <-media.Done():
	default:
		t.Fatal("the read loop did not signal Done")
	}
	if !socket.isClosed() {
		t.Fatal("the WebSocket stayed open after a fatal media error")
	}
	if health := media.Health(); health.TransportAlive || health.DeviceReady {
		t.Fatalf("health still reports the channel up: %+v", health)
	}
}
