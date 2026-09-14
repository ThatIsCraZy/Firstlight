package rfb

import (
	"encoding/binary"
	"image/color"
)

// PixelFormat mirrors the 16-byte PIXEL_FORMAT structure of RFC 6143.
type PixelFormat struct {
	BitsPerPixel uint8
	Depth        uint8
	BigEndian    bool
	TrueColor    bool
	RedMax       uint16
	GreenMax     uint16
	BlueMax      uint16
	RedShift     uint8
	GreenShift   uint8
	BlueShift    uint8
}

// RGB565 packs five bits of red, six of green and five of blue into a
// little-endian 16-bit word.
//
// This is the format Dell's firmware actually produces. Its ServerInit
// announces shifts 11/6/0 with a maximum of 31 on every channel, which cannot
// describe any real layout: a green channel starting at bit 6 with five bits
// would end at bit 10 and leave bit 5 unused. Measured against a live
// controller, the green channel is six bits wide and starts at bit 5, so the
// announcement is simply wrong. Asking for this format explicitly makes every
// encoding decode correctly, while the 5-5-5 layout below only works for Raw:
// the firmware converts Raw to whatever the client asks for but emits Hextile
// in its native 5-6-5 regardless.
var RGB565 = PixelFormat{
	BitsPerPixel: 16,
	Depth:        16,
	TrueColor:    true,
	RedMax:       31,
	GreenMax:     63,
	BlueMax:      31,
	RedShift:     11,
	GreenShift:   5,
	BlueShift:    0,
}

// Depth16 is the five-bits-per-channel layout noVNC requests for depth 16.
var Depth16 = PixelFormat{
	BitsPerPixel: 16,
	Depth:        16,
	TrueColor:    true,
	RedMax:       31,
	GreenMax:     31,
	BlueMax:      31,
	RedShift:     10,
	GreenShift:   5,
	BlueShift:    0,
}

// BytesPerPixel reports how many bytes one pixel occupies on the wire.
func (p PixelFormat) BytesPerPixel() int {
	return int(p.BitsPerPixel) / 8
}

func (p PixelFormat) parse(b []byte) PixelFormat {
	return PixelFormat{
		BitsPerPixel: b[0],
		Depth:        b[1],
		BigEndian:    b[2] != 0,
		TrueColor:    b[3] != 0,
		RedMax:       binary.BigEndian.Uint16(b[4:6]),
		GreenMax:     binary.BigEndian.Uint16(b[6:8]),
		BlueMax:      binary.BigEndian.Uint16(b[8:10]),
		RedShift:     b[10],
		GreenShift:   b[11],
		BlueShift:    b[12],
	}
}

func (p PixelFormat) encode() []byte {
	out := make([]byte, 16)
	out[0] = p.BitsPerPixel
	out[1] = p.Depth
	if p.BigEndian {
		out[2] = 1
	}
	if p.TrueColor {
		out[3] = 1
	}
	binary.BigEndian.PutUint16(out[4:6], p.RedMax)
	binary.BigEndian.PutUint16(out[6:8], p.GreenMax)
	binary.BigEndian.PutUint16(out[8:10], p.BlueMax)
	out[10] = p.RedShift
	out[11] = p.GreenShift
	out[12] = p.BlueShift
	return out
}

// raw reads one pixel as an unsigned integer in the server's byte order.
func (p PixelFormat) raw(b []byte) uint32 {
	switch p.BytesPerPixel() {
	case 1:
		return uint32(b[0])
	case 2:
		if p.BigEndian {
			return uint32(binary.BigEndian.Uint16(b))
		}
		return uint32(binary.LittleEndian.Uint16(b))
	case 4:
		if p.BigEndian {
			return binary.BigEndian.Uint32(b)
		}
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

// Color converts one on-the-wire pixel to RGBA, scaling each channel to the
// full 0..255 range so a 5-bit channel does not come out dim.
func (p PixelFormat) Color(b []byte) color.RGBA {
	v := p.raw(b)
	return color.RGBA{
		R: scale(v>>p.RedShift&uint32(p.RedMax), p.RedMax),
		G: scale(v>>p.GreenShift&uint32(p.GreenMax), p.GreenMax),
		B: scale(v>>p.BlueShift&uint32(p.BlueMax), p.BlueMax),
		A: 255,
	}
}

func scale(value uint32, max uint16) uint8 {
	if max == 0 {
		return 0
	}
	return uint8(value * 255 / uint32(max))
}
