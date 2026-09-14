package kvm

import (
	"image/color"
	"testing"
)

func TestFramebufferStorePixel(t *testing.T) {
	f := NewFramebuffer(2, 2)
	want := color.RGBA{R: 1, G: 2, B: 3, A: 255}
	f.StorePixel(1, 1, want)
	if got := f.Image().RGBAAt(1, 1); got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestDecoderEmptyFeed(t *testing.T) {
	d := NewDecoder(2, 2)
	if err := d.Feed(nil); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestDecoderReadyToWriteAfterHeader(t *testing.T) {
	d := NewDecoder(2, 2)
	if d.ReadyToWrite() {
		t.Fatal("decoder was ready before header")
	}
	d.processHeader([]byte{0, byte(LegacyCipherAES128), 0, 0})
	if !d.ReadyToWrite() {
		t.Fatal("decoder was not ready after header")
	}
	if d.Encryption() != LegacyCipherAES128 {
		t.Fatalf("encryption=%d want=%d", d.Encryption(), LegacyCipherAES128)
	}
	if d.EncryptionID() != 1 {
		t.Fatalf("encryption id=%d want=1", d.EncryptionID())
	}
	if d.FrameRevision() != 1 {
		t.Fatalf("initial frame revision=%d want=1", d.FrameRevision())
	}
	d.processHeader([]byte{0, byte(LegacyCipherAES128), 0, 0})
	if d.FrameRevision() != 1 {
		t.Fatalf("repeated header changed frame revision to %d", d.FrameRevision())
	}
}

// feedCommand replays one firmware command the way the wire carries it:
// parameters first, opcode last.
func feedCommand(d *Decoder, opcode byte, params ...byte) {
	d.cmdCount = 0
	for _, p := range params {
		d.pushCommandOctet(p)
	}
	d.pushCommandOctet(opcode)
}

func TestDecoderReadsParametersBeforeTheOpcode(t *testing.T) {
	d := NewDecoder(2, 2)
	feedCommand(d, 13, 1, 2, 3, 4)
	if got := d.commandParams(); string(got) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("params=%v want=[1 2 3 4]", got)
	}
	if d.cmdLast != 13 {
		t.Fatalf("opcode=%d want=13", d.cmdLast)
	}
	feedCommand(d, 6)
	if got := d.commandParams(); len(got) != 0 {
		t.Fatalf("a bare opcode reported params=%v", got)
	}
}

func TestDecoderTracksEncryptionCommand(t *testing.T) {
	d := NewDecoder(2, 2)
	feedCommand(d, 12, byte(LegacyCipherRC4))
	if !d.processCommand() {
		t.Fatal("encryption command was not processed")
	}
	if d.Encryption() != LegacyCipherRC4 {
		t.Fatalf("encryption=%d want=%d", d.Encryption(), LegacyCipherRC4)
	}
	firstID := d.EncryptionID()
	feedCommand(d, 12, byte(LegacyCipherRC4))
	if !d.processCommand() || d.EncryptionID() != firstID+1 {
		t.Fatal("repeated encryption command did not request a stream reset")
	}
}

func TestDecoderIgnoresACommandThatCarriesNoParameter(t *testing.T) {
	d := NewDecoder(2, 2)
	feedCommand(d, 12, byte(LegacyCipherAES256))
	d.processCommand()
	settled := d.EncryptionID()

	// A bare opcode 12. The buffer still holds AES256 from the command above,
	// which must not be read as this command's parameter.
	feedCommand(d, 12)
	if !d.processCommand() {
		t.Fatal("bare encryption command was not processed")
	}
	if d.EncryptionID() != settled {
		t.Fatalf("encryption id moved to %d; a stale parameter was applied", d.EncryptionID())
	}
	if d.Encryption() != LegacyCipherAES256 {
		t.Fatalf("encryption=%d want=%d", d.Encryption(), LegacyCipherAES256)
	}

	// The same for the header command, whose parameters are four octets.
	before := d.bitsPerColor
	feedCommand(d, 13, 9)
	if !d.processCommand() {
		t.Fatal("short header command was not processed")
	}
	if d.bitsPerColor != before {
		t.Fatalf("short header applied bpc=%d", d.bitsPerColor)
	}
}

func TestDecoderCopiesCompletedBlock(t *testing.T) {
	d := NewDecoder(16, 16)
	d.sizeX = 1
	d.sizeY = 1
	want := color.RGBA{R: 255, G: 1, B: 2, A: 255}
	d.block[0] = want
	d.nextBlock(1)
	if got := d.Framebuffer.Image().RGBAAt(0, 0); got != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
	firstRevision := d.FrameRevision()
	d.lastX = 0
	d.lastY = 0
	d.nextBlock(1)
	if d.FrameRevision() != firstRevision {
		t.Fatalf("identical block changed frame revision from %d to %d", firstRevision, d.FrameRevision())
	}
}

func TestDecoderPrintCommandEntersPrintStates(t *testing.T) {
	d := NewDecoder(16, 16)
	d.cmdLast = 2
	if !d.processCommand() {
		t.Fatal("print command was not processed")
	}
	if d.nextState != 44 {
		t.Fatalf("next state=%d want=44 (PRINT0)", d.nextState)
	}
}

func TestDecoderConsumesFirmwareMessage(t *testing.T) {
	d := NewDecoder(16, 16)
	var gotTag byte
	var gotText string
	d.SetFirmwareMessageHandler(func(tag byte, text string) {
		gotTag, gotText = tag, text
	})

	// PRINT0 takes the tag byte, PRINT1 the NUL-terminated text. Both read
	// eight bits, which arrive bit-reversed.
	d.decoderState = 44
	stream := []byte{d.reversal[3]}
	for _, c := range []byte("iLO reset") {
		stream = append(stream, d.reversal[c])
	}
	stream = append(stream, 0)
	if err := d.Feed(stream); err != nil {
		t.Fatalf("feed: %v", err)
	}

	if gotTag != 3 || gotText != "iLO reset" {
		t.Fatalf("tag=%d text=%q want tag=3 text=%q", gotTag, gotText, "iLO reset")
	}
	if d.decoderState != 1 {
		t.Fatalf("decoder state after message=%d want=1 (START)", d.decoderState)
	}
	if d.FrameRevision() != 0 {
		t.Fatalf("message text reached the framebuffer: revision=%d", d.FrameRevision())
	}
}
