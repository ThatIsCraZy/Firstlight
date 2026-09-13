package kvm

import (
	"image"
	"image/color"
	"sync"
	"testing"
)

func TestVideoStreamCopyFrameIsPrivate(t *testing.T) {
	v := NewVideoStream(16, 16)
	want := color.RGBA{R: 9, G: 8, B: 7, A: 255}
	v.decoder.Framebuffer.StorePixel(1, 1, want)

	dst := v.CopyFrame(nil)
	if got := dst.RGBAAt(1, 1); got != want {
		t.Fatalf("copy got %#v want %#v", got, want)
	}

	// A later decoder write must not reach a frame already handed out.
	v.decoder.Framebuffer.StorePixel(1, 1, color.RGBA{R: 1, A: 255})
	if got := dst.RGBAAt(1, 1); got != want {
		t.Fatalf("decoder wrote into the handed-out frame: %#v", got)
	}
}

func TestVideoStreamCopyFrameReusesBuffer(t *testing.T) {
	v := NewVideoStream(16, 16)
	first := v.CopyFrame(nil)
	if second := v.CopyFrame(first); second != first {
		t.Fatal("matching bounds allocated a new buffer")
	}
	small := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if got := v.CopyFrame(small); got == small {
		t.Fatal("mismatched bounds reused the buffer")
	}
}

func TestVideoStreamFeedAndCopyAreSerialized(t *testing.T) {
	v := NewVideoStream(16, 16)
	stream := []byte{v.decoder.reversal[3]}
	for _, c := range []byte("iLO") {
		stream = append(stream, v.decoder.reversal[c])
	}
	stream = append(stream, 0)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			v.Feed(stream)
		}
	}()
	go func() {
		defer wg.Done()
		var dst *image.RGBA
		for i := 0; i < 200; i++ {
			dst = v.CopyFrame(dst)
		}
	}()
	wg.Wait()
}

func TestDecoderRequestsRefreshWhenLatched(t *testing.T) {
	d := NewDecoder(16, 16)
	// LATCHED reads one bit and stays put, so one byte drives eight passes.
	d.decoderState = 38
	d.fatalCount = 32768
	if err := d.Feed([]byte{0xff}); err != nil {
		t.Fatalf("feed: %v", err)
	}
	if d.RefreshRequests() != 1 {
		t.Fatalf("refresh requests=%d want=1", d.RefreshRequests())
	}
	if d.fatalCount != 8 {
		t.Fatalf("fatal count=%d want=8 (reset, then one per pass)", d.fatalCount)
	}
	if d.decoderState != 38 {
		t.Fatalf("decoder state=%d want=38 (LATCHED)", d.decoderState)
	}
}
