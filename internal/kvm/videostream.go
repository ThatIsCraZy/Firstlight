package kvm

import (
	"image"
	"sync"
)

// FeedResult reports what one Feed changed, captured while the stream was
// locked. Callers act on it after the lock is gone, so no network write or UI
// update happens with the decoder blocked.
type FeedResult struct {
	Err               error
	Ready             bool
	EncryptionChanged bool
	Encryption        LegacyCipher
	RefreshRequested  bool
	FrameRevision     uint64
	Bounds            image.Rectangle
}

// VideoStream owns a Decoder together with the lock between the goroutine that
// feeds the video channel and the one that reads frames out of it. Sessions
// drive the decoder only through this type, so neither can reach the live
// framebuffer and both inherit the same locking.
type VideoStream struct {
	mu      sync.Mutex
	decoder *Decoder
}

func NewVideoStream(w, h int) *VideoStream {
	return &VideoStream{decoder: NewDecoder(w, h)}
}

// SetFirmwareMessageHandler installs the sink for firmware print commands. The
// handler runs inside Feed with the stream locked, so it must not block.
func (v *VideoStream) SetFirmwareMessageHandler(fn FirmwareMessageFunc) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.decoder.SetFirmwareMessageHandler(fn)
}

// Feed decodes one read from the video channel.
func (v *VideoStream) Feed(p []byte) FeedResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	previousEncryptionID := v.decoder.EncryptionID()
	previousRefresh := v.decoder.RefreshRequests()
	err := v.decoder.Feed(p)
	return FeedResult{
		Err:               err,
		Ready:             v.decoder.ReadyToWrite(),
		EncryptionChanged: v.decoder.EncryptionID() != previousEncryptionID,
		Encryption:        v.decoder.Encryption(),
		RefreshRequested:  v.decoder.RefreshRequests() != previousRefresh,
		FrameRevision:     v.decoder.FrameRevision(),
		Bounds:            v.decoder.Framebuffer.Image().Bounds(),
	}
}

// CopyFrame copies the framebuffer into dst, reusing it while the bounds match
// and allocating otherwise. The returned buffer belongs to the caller; Feed
// cannot write into it.
func (v *VideoStream) CopyFrame(dst *image.RGBA) *image.RGBA {
	v.mu.Lock()
	defer v.mu.Unlock()
	src := v.decoder.Framebuffer.Image()
	if dst == nil || !dst.Rect.Eq(src.Rect) {
		dst = image.NewRGBA(src.Rect)
	}
	copy(dst.Pix, src.Pix)
	return dst
}
