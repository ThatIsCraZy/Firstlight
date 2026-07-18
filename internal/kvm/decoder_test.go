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
	d.processHeader([]byte{0, 0, 0, 0})
	if !d.ReadyToWrite() {
		t.Fatal("decoder was not ready after header")
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
}
