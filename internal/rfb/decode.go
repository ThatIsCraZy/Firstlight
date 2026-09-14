package rfb

import (
	"encoding/binary"
	"fmt"
	"image/color"
)

// Hextile subencoding bits, RFC 6143 section 7.7.4.
const (
	hextileRaw                 = 0x01
	hextileBackgroundSpecified = 0x02
	hextileForegroundSpecified = 0x04
	hextileAnySubrects         = 0x08
	hextileSubrectsColoured    = 0x10
)

type rectangle struct {
	x, y, w, h int
	encoding   int32
}

func (c *Conn) readFramebufferUpdate() (*Update, error) {
	header, err := c.read(3)
	if err != nil {
		return nil, err
	}
	count := int(binary.BigEndian.Uint16(header[1:3]))
	// 0xffff means the server does not know the count yet and will close the
	// update with a LastRect pseudo-rectangle.
	limit := count
	if count == 0xffff {
		limit = maxRectanglesPerFB
	} else if count > maxRectanglesPerFB {
		return nil, fmt.Errorf("framebuffer update declares %d rectangles", count)
	}
	update := &Update{}
	for i := 0; i < limit; i++ {
		raw, err := c.read(12)
		if err != nil {
			return nil, err
		}
		rect := rectangle{
			x:        int(binary.BigEndian.Uint16(raw[0:2])),
			y:        int(binary.BigEndian.Uint16(raw[2:4])),
			w:        int(binary.BigEndian.Uint16(raw[4:6])),
			h:        int(binary.BigEndian.Uint16(raw[6:8])),
			encoding: int32(binary.BigEndian.Uint32(raw[8:12])),
		}
		c.log("rect %dx%d at %d,%d encoding=%s", rect.w, rect.h, rect.x, rect.y, EncodingName(rect.encoding))
		done, resized, err := c.decodeRectangle(rect)
		if err != nil {
			return nil, fmt.Errorf("decode %s rectangle: %w", EncodingName(rect.encoding), err)
		}
		if resized {
			update.Resized = true
		}
		update.Rectangles++
		if done {
			break
		}
	}
	c.screen.Commit()
	return update, nil
}

// decodeRectangle applies one rectangle. The first return value reports
// whether the update ended early, which LastRect signals.
func (c *Conn) decodeRectangle(rect rectangle) (done, resized bool, err error) {
	switch rect.encoding {
	case EncodingLastRect:
		return true, false, nil
	case EncodingDesktop:
		c.screen.Resize(rect.w, rect.h)
		return false, true, nil
	case EncodingCursor:
		// The cursor shape is drawn locally, so the data is read and dropped.
		return false, false, c.skipCursor(rect)
	case EncodingRaw:
		return false, false, c.decodeRaw(rect)
	case EncodingCopyRect:
		return false, false, c.decodeCopyRect(rect)
	case EncodingRRE:
		return false, false, c.decodeRRE(rect)
	case EncodingHextile:
		return false, false, c.decodeHextile(rect)
	default:
		return false, false, fmt.Errorf("encoding %d is not implemented", rect.encoding)
	}
}

func (c *Conn) decodeRaw(rect rectangle) error {
	bpp := c.format.BytesPerPixel()
	if bpp == 0 {
		return fmt.Errorf("pixel format reports %d bits per pixel", c.format.BitsPerPixel)
	}
	data, err := c.read(rect.w * rect.h * bpp)
	if err != nil {
		return err
	}
	c.screen.mu.Lock()
	defer c.screen.mu.Unlock()
	offset := 0
	for y := 0; y < rect.h; y++ {
		for x := 0; x < rect.w; x++ {
			c.screen.setPixel(rect.x+x, rect.y+y, c.format.Color(data[offset:offset+bpp]))
			offset += bpp
		}
	}
	return nil
}

func (c *Conn) decodeCopyRect(rect rectangle) error {
	raw, err := c.read(4)
	if err != nil {
		return err
	}
	srcX := int(binary.BigEndian.Uint16(raw[0:2]))
	srcY := int(binary.BigEndian.Uint16(raw[2:4]))
	c.screen.mu.Lock()
	defer c.screen.mu.Unlock()
	c.screen.copyRect(srcX, srcY, rect.x, rect.y, rect.w, rect.h)
	return nil
}

func (c *Conn) decodeRRE(rect rectangle) error {
	bpp := c.format.BytesPerPixel()
	header, err := c.read(4 + bpp)
	if err != nil {
		return err
	}
	count := binary.BigEndian.Uint32(header[0:4])
	if count > maxRectanglesPerFB*16 {
		return fmt.Errorf("RRE declares %d subrectangles", count)
	}
	background := c.format.Color(header[4:])
	body, err := c.read(int(count) * (bpp + 8))
	if err != nil {
		return err
	}
	c.screen.mu.Lock()
	defer c.screen.mu.Unlock()
	c.screen.fill(rect.x, rect.y, rect.w, rect.h, background)
	offset := 0
	for i := uint32(0); i < count; i++ {
		pixel := c.format.Color(body[offset : offset+bpp])
		offset += bpp
		x := int(binary.BigEndian.Uint16(body[offset : offset+2]))
		y := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		w := int(binary.BigEndian.Uint16(body[offset+4 : offset+6]))
		h := int(binary.BigEndian.Uint16(body[offset+6 : offset+8]))
		offset += 8
		c.screen.fill(rect.x+x, rect.y+y, w, h, pixel)
	}
	return nil
}

func (c *Conn) decodeHextile(rect rectangle) error {
	bpp := c.format.BytesPerPixel()
	var background, foreground color.RGBA
	for tileY := 0; tileY < rect.h; tileY += 16 {
		tileH := min(16, rect.h-tileY)
		for tileX := 0; tileX < rect.w; tileX += 16 {
			tileW := min(16, rect.w-tileX)
			flagByte, err := c.read(1)
			if err != nil {
				return err
			}
			flags := flagByte[0]
			if flags&hextileRaw != 0 {
				data, err := c.read(tileW * tileH * bpp)
				if err != nil {
					return err
				}
				c.screen.mu.Lock()
				offset := 0
				for y := 0; y < tileH; y++ {
					for x := 0; x < tileW; x++ {
						c.screen.setPixel(rect.x+tileX+x, rect.y+tileY+y,
							c.format.Color(data[offset:offset+bpp]))
						offset += bpp
					}
				}
				c.screen.mu.Unlock()
				continue
			}
			if flags&hextileBackgroundSpecified != 0 {
				raw, err := c.read(bpp)
				if err != nil {
					return err
				}
				background = c.format.Color(raw)
			}
			if flags&hextileForegroundSpecified != 0 {
				raw, err := c.read(bpp)
				if err != nil {
					return err
				}
				foreground = c.format.Color(raw)
			}
			c.screen.mu.Lock()
			c.screen.fill(rect.x+tileX, rect.y+tileY, tileW, tileH, background)
			c.screen.mu.Unlock()
			if flags&hextileAnySubrects == 0 {
				continue
			}
			countByte, err := c.read(1)
			if err != nil {
				return err
			}
			count := int(countByte[0])
			size := 2
			if flags&hextileSubrectsColoured != 0 {
				size += bpp
			}
			body, err := c.read(count * size)
			if err != nil {
				return err
			}
			c.screen.mu.Lock()
			offset := 0
			for i := 0; i < count; i++ {
				pixel := foreground
				if flags&hextileSubrectsColoured != 0 {
					pixel = c.format.Color(body[offset : offset+bpp])
					offset += bpp
				}
				position := body[offset]
				extent := body[offset+1]
				offset += 2
				subX := int(position >> 4)
				subY := int(position & 0x0f)
				subW := int(extent>>4) + 1
				subH := int(extent&0x0f) + 1
				c.screen.fill(rect.x+tileX+subX, rect.y+tileY+subY, subW, subH, pixel)
			}
			c.screen.mu.Unlock()
		}
	}
	return nil
}

func (c *Conn) skipCursor(rect rectangle) error {
	bpp := c.format.BytesPerPixel()
	maskLength := ((rect.w + 7) / 8) * rect.h
	_, err := c.read(rect.w*rect.h*bpp + maskLength)
	return err
}

// EncodingName renders an encoding number for log and error messages.
func EncodingName(encoding int32) string {
	switch encoding {
	case EncodingRaw:
		return "Raw"
	case EncodingCopyRect:
		return "CopyRect"
	case EncodingRRE:
		return "RRE"
	case EncodingHextile:
		return "Hextile"
	case EncodingCursor:
		return "Cursor"
	case EncodingDesktop:
		return "DesktopSize"
	case EncodingLastRect:
		return "LastRect"
	}
	return fmt.Sprintf("encoding(%d)", encoding)
}
