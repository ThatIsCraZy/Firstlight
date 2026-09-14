package rfb

import (
	"image"
	"image/color"
	"sync"
)

// Screen holds the framebuffer a session paints into. It carries its own lock
// so the decode loop and the user interface can touch it from two goroutines.
type Screen struct {
	mu       sync.RWMutex
	img      *image.RGBA
	revision uint64
}

// NewScreen allocates a framebuffer of the given size.
func NewScreen(w, h int) *Screen {
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	return &Screen{img: image.NewRGBA(image.Rect(0, 0, w, h))}
}

// Bounds reports the current framebuffer size.
func (s *Screen) Bounds() image.Rectangle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.img.Bounds()
}

// Revision counts completed framebuffer updates.
func (s *Screen) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

// Commit marks the end of one framebuffer update.
func (s *Screen) Commit() {
	s.mu.Lock()
	s.revision++
	s.mu.Unlock()
}

// Resize replaces the framebuffer, keeping the overlapping top-left region so
// a desktop-size change does not blank the window.
func (s *Screen) Resize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.img.Bounds().Dx() == w && s.img.Bounds().Dy() == h {
		return
	}
	next := image.NewRGBA(image.Rect(0, 0, w, h))
	copyRegion(next, s.img, min(w, s.img.Bounds().Dx()), min(h, s.img.Bounds().Dy()))
	s.img = next
}

// CopyFrame returns a private copy of the framebuffer, reusing dst when it
// already has the right size.
func (s *Screen) CopyFrame(dst *image.RGBA) *image.RGBA {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bounds := s.img.Bounds()
	if dst == nil || dst.Bounds() != bounds {
		dst = image.NewRGBA(bounds)
	}
	copy(dst.Pix, s.img.Pix)
	dst.Stride = s.img.Stride
	return dst
}

// setPixel writes one pixel, ignoring coordinates outside the framebuffer.
// The caller holds the write lock through a paint batch.
func (s *Screen) setPixel(x, y int, c color.RGBA) {
	b := s.img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
		return
	}
	offset := s.img.PixOffset(x, y)
	s.img.Pix[offset] = c.R
	s.img.Pix[offset+1] = c.G
	s.img.Pix[offset+2] = c.B
	s.img.Pix[offset+3] = 255
}

// fill paints a solid rectangle.
func (s *Screen) fill(x, y, w, h int, c color.RGBA) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			s.setPixel(x+dx, y+dy, c)
		}
	}
}

// copyRect moves a region inside the framebuffer, which is what the CopyRect
// encoding asks for when a window scrolls.
func (s *Screen) copyRect(srcX, srcY, dstX, dstY, w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	// A scratch copy keeps overlapping source and destination correct.
	scratch := make([]color.RGBA, 0, w*h)
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			scratch = append(scratch, s.at(srcX+dx, srcY+dy))
		}
	}
	i := 0
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			s.setPixel(dstX+dx, dstY+dy, scratch[i])
			i++
		}
	}
}

func (s *Screen) at(x, y int) color.RGBA {
	b := s.img.Bounds()
	if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
		return color.RGBA{A: 255}
	}
	offset := s.img.PixOffset(x, y)
	return color.RGBA{
		R: s.img.Pix[offset],
		G: s.img.Pix[offset+1],
		B: s.img.Pix[offset+2],
		A: 255,
	}
}

func copyRegion(dst, src *image.RGBA, w, h int) {
	for y := 0; y < h; y++ {
		copy(dst.Pix[dst.PixOffset(0, y):dst.PixOffset(0, y)+w*4],
			src.Pix[src.PixOffset(0, y):src.PixOffset(0, y)+w*4])
	}
}
