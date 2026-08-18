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

func TestDecoderTracksEncryptionCommand(t *testing.T) {
	d := NewDecoder(2, 2)
	d.cmdLast = 12
	d.cmdCount = 1
	d.cmdBuff[0] = byte(LegacyCipherRC4)
	if !d.processCommand() {
		t.Fatal("encryption command was not processed")
	}
	if d.Encryption() != LegacyCipherRC4 {
		t.Fatalf("encryption=%d want=%d", d.Encryption(), LegacyCipherRC4)
	}
	firstID := d.EncryptionID()
	d.cmdBuff[0] = byte(LegacyCipherRC4)
	if !d.processCommand() || d.EncryptionID() != firstID+1 {
		t.Fatal("repeated encryption command did not request a stream reset")
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
