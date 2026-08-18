package kvm

import (
	"errors"
	"image"
	"image/color"
)

var ErrUnsupportedVideoPacket = errors.New("unsupported KVM video packet")

type Framebuffer struct {
	img *image.RGBA
}

func NewFramebuffer(w, h int) *Framebuffer {
	if w <= 0 {
		w = 800
	}
	if h <= 0 {
		h = 600
	}
	f := &Framebuffer{img: image.NewRGBA(image.Rect(0, 0, w, h))}
	f.Clear(color.RGBA{R: 8, G: 10, B: 12, A: 255})
	return f
}

func (f *Framebuffer) Image() *image.RGBA {
	return f.img
}

func (f *Framebuffer) Clear(c color.RGBA) bool {
	changed := false
	for y := f.img.Rect.Min.Y; y < f.img.Rect.Max.Y; y++ {
		for x := f.img.Rect.Min.X; x < f.img.Rect.Max.X; x++ {
			if f.img.RGBAAt(x, y) != c {
				f.img.SetRGBA(x, y, c)
				changed = true
			}
		}
	}
	return changed
}

func (f *Framebuffer) StorePixel(x, y int, c color.RGBA) bool {
	if !image.Pt(x, y).In(f.img.Rect) || f.img.RGBAAt(x, y) == c {
		return false
	}
	f.img.SetRGBA(x, y, c)
	return true
}

type stateLine struct {
	name       string
	bitsToRead uint32
	state0     int
	state1     int
}

type colorElement struct {
	color        uint16
	usage        uint16
	blockCounter int
}

type colorCache struct {
	active  int
	element [17]colorElement
	pixcode int
}

func newColorCache() colorCache {
	return colorCache{pixcode: 38}
}

func (c *colorCache) reset() {
	c.active = 0
	for i := range c.element {
		c.element[i].usage = 0
		c.element[i].blockCounter = 0
	}
	c.pixcode = 38
}

func (c *colorCache) lru(col uint16, indx, length, lru *uint16) int {
	lruLengths := [...]uint16{0, 0, 0, 1, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 0}
	active := c.active
	if active > len(c.element) {
		active = len(c.element)
		c.active = active
	}
	slot := 0
	found := 0
	for i := 0; i < active; i++ {
		if col == c.element[i].color {
			*length = lruLengths[active]
			*indx = uint16(i)
			*lru = c.element[i].usage
			slot = i
			found = 1
			break
		}
		if int(c.element[i].usage) == active-1 {
			slot = i
		}
	}
	oldUsage := c.element[slot].usage
	if found == 0 {
		if active < len(c.element) {
			slot = active
			oldUsage = uint16(active)
			active++
			c.active = active
			c.updatePixcode()
		}
		c.element[slot].color = col
	}
	c.element[slot].blockCounter = 1
	for i := 0; i < active; i++ {
		if c.element[i].usage < oldUsage {
			c.element[i].usage++
		}
	}
	c.element[slot].usage = 0
	return found
}

func (c *colorCache) find(code uint16, col *uint16) int {
	active := c.active
	if active > len(c.element) {
		active = len(c.element)
		c.active = active
	}
	for i := 0; i < active; i++ {
		if code != c.element[i].usage {
			continue
		}
		*col = c.element[i].color
		slot := i
		for j := 0; j < active; j++ {
			if c.element[j].usage < code {
				c.element[j].usage++
			}
		}
		c.element[slot].usage = 0
		c.element[slot].blockCounter = 1
		return 0
	}
	return 1
}

func (c *colorCache) prune() {
	active := c.active
	if active > len(c.element) {
		active = len(c.element)
	}
	i := 0
	for i < active {
		if c.element[i].blockCounter == 0 {
			active--
			c.element[i] = c.element[active]
			continue
		}
		c.element[i].blockCounter--
		i++
	}
	c.active = active
	c.updatePixcode()
}

func (c *colorCache) updatePixcode() {
	switch {
	case c.active < 2:
		c.pixcode = 38
	case c.active == 2:
		c.pixcode = 4
	case c.active == 3:
		c.pixcode = 5
	case c.active < 6:
		c.pixcode = 6
	case c.active < 10:
		c.pixcode = 7
	default:
		c.pixcode = 32
	}
}

type Decoder struct {
	Framebuffer *Framebuffer

	buf     []byte
	readPtr int

	stateTable []stateLine
	cache      colorCache
	reversal   [256]byte
	right      [256]uint32
	left       [256]uint32
	getmask    [9]byte

	block             []color.RGBA
	pixelWidth        int
	pixelHeight       int
	blockHeight       int
	blockWidth        int
	halfHeightCapable bool

	decoderState int
	nextState    int
	code         byte

	ibAcc      uint32
	ibBcnt     uint32
	zeroCount  uint32
	countBytes uint32
	fatalCount uint32

	bitsPerColor uint32
	pixelCount   uint32
	sizeX        byte
	sizeY        byte
	sizeY1       byte
	lastX        byte
	lastY        byte
	newX         byte
	newY         byte
	color        uint16
	lastColor    uint16

	timeoutCount  int
	cmdBuff       [256]byte
	cmdCount      int
	cmdLast       byte
	halt          bool
	readyToWrite  bool
	frameRevision uint64
	encryption    LegacyCipher
	encryptionID  uint64
}

func NewDecoder(w, h int) *Decoder {
	fb := NewFramebuffer(w, h)
	d := &Decoder{
		Framebuffer:  fb,
		pixelWidth:   fb.img.Bounds().Dx(),
		pixelHeight:  fb.img.Bounds().Dy(),
		blockHeight:  16,
		blockWidth:   16,
		bitsPerColor: 5,
		timeoutCount: -1,
		getmask:      [9]byte{0, 1, 3, 7, 15, 31, 63, 127, 255},
		cache:        newColorCache(),
	}
	d.initRemCons()
	d.reinitStateMachine()
	return d
}

func (d *Decoder) Feed(packet []byte) error {
	if len(packet) == 0 || d.halt {
		return nil
	}
	d.buf = append(d.buf, packet...)
	limit := (len(d.buf) + 1) * 2048
	for steps := 0; steps < limit; steps++ {
		ok, err := d.step()
		if err != nil {
			return err
		}
		if !ok || d.halt {
			break
		}
	}
	d.compact()
	return nil
}

func (d *Decoder) ReadyToWrite() bool {
	return d.readyToWrite
}

func (d *Decoder) FrameRevision() uint64 {
	return d.frameRevision
}

func (d *Decoder) Encryption() LegacyCipher {
	return d.encryption
}

func (d *Decoder) EncryptionID() uint64 {
	return d.encryptionID
}

func (d *Decoder) initRemCons() {
	d.stateTable = []stateLine{
		{"RESET", 0, 1, 1},
		{"START", 1, 2, 15},
		{"PIXELS", 1, 31, 3},
		{"PIXLRU1", 1, 2, 11},
		{"PIXLRU0", 1, 2, 11},
		{"PIXCODE1", 1, 10, 10},
		{"PIXCODE2", 2, 10, 10},
		{"PIXCODE3", 3, 10, 10},
		{"PIXGREY", d.bitsPerColor, 10, 10},
		{"PIXRGBR", d.bitsPerColor, 41, 41},
		{"PIXRPT", 1, 2, 11},
		{"PIXRPT1", 1, 33, 12},
		{"PIXRPTSTD1", 3, 2, 2},
		{"PIXRPTSTD2", 3, 2, 2},
		{"PIXRPTNSTD", 8, 2, 2},
		{"CMD", 1, 16, 17},
		{"CMD0", 1, 19, 18},
		{"MOVEXY0", 7, 39, 39},
		{"EXTCMD", 1, 22, 23},
		{"CMDX", 1, 20, 21},
		{"MOVESHORTX", 3, 1, 1},
		{"MOVELONGX", 7, 1, 1},
		{"BLKRPT", 1, 34, 28},
		{"EXTCMD1", 1, 25, 24},
		{"FIRMWARE", 8, 46, 46},
		{"EXTCMD2", 1, 26, 27},
		{"MODE0", 7, 40, 40},
		{"TIMEOUT", 0, 1, 1},
		{"BLKRPT1", 1, 29, 30},
		{"BLKRPTSTD", 3, 1, 1},
		{"BLKRPTNSTD", 7, 1, 1},
		{"PIXFAN", 1, 36, 35},
		{"PIXCODE4", 4, 10, 10},
		{"PIXDUP", 0, 2, 2},
		{"BLKDUP", 0, 1, 1},
		{"PIXCODE", 0, 35, 35},
		{"PIXSPEC", 1, 8, 9},
		{"EXIT", 0, 37, 37},
		{"LATCHED", 1, 38, 38},
		{"MOVEXY1", 7, 1, 1},
		{"MODE1", 7, 47, 47},
		{"PIXRGBG", d.bitsPerColor, 42, 42},
		{"PIXRGBB", d.bitsPerColor, 10, 10},
		{"HUNT", 1, 43, 0},
		{"PRINT0", 8, 45, 45},
		{"PRINT1", 8, 45, 45},
		{"CORP", 1, 1, 24},
		{"MODE2", 4, 1, 1},
	}
	d.block = make([]color.RGBA, d.blockHeight*d.blockWidth)
	d.initReversal()
}

func (d *Decoder) initReversal() {
	for i := 0; i < 256; i++ {
		right := byte(8)
		left := byte(8)
		v := byte(i)
		var rev byte
		for bit := 0; bit < 8; bit++ {
			rev <<= 1
			if v&1 != 0 {
				if right > byte(bit) {
					right = byte(bit)
				}
				rev |= 1
				left = byte(7 - bit)
			}
			v >>= 1
		}
		d.reversal[i] = rev
		d.right[i] = uint32(right)
		d.left[i] = uint32(left)
	}
}

func (d *Decoder) reinitStateMachine() {
	d.initReversal()
	d.cache.reset()
	d.readPtr = 0
	d.decoderState = 0
	d.nextState = 0
	d.zeroCount = 0
	d.countBytes = 0
	d.ibAcc = 0
	d.ibBcnt = 0
	d.sizeX = 0
	d.sizeY = 0
	d.code = 0
	d.halt = false
}

func (d *Decoder) getByte() (byte, bool) {
	if d.readPtr >= len(d.buf) {
		return 0, false
	}
	b := d.buf[d.readPtr]
	d.readPtr++
	return b, true
}

func (d *Decoder) getBit(length uint32) (int, bool) {
	if length == 0 {
		return 0, true
	}
	if length >= uint32(len(d.getmask)) {
		return 0, false
	}
	var b byte
	if d.ibBcnt < length {
		var ok bool
		b, ok = d.getByte()
		if !ok {
			return 0, false
		}
		d.zeroCount += d.right[b]
		if d.zeroCount > 30 {
			for b == 0 {
				b, ok = d.getByte()
				if !ok {
					return 0, false
				}
			}
			d.ibBcnt = 0
			d.ibAcc = 0
			d.zeroCount = d.right[b]
			d.decoderState = 0
			d.nextState = 0
			return 4, true
		}
		if b != 0 {
			d.zeroCount = d.left[b]
		}
		d.ibAcc |= uint32(b) << (d.ibBcnt & 31)
		d.ibBcnt += 8
	}
	b = byte(d.ibAcc & uint32(d.getmask[length]))
	d.ibBcnt -= length
	d.ibAcc >>= length & 31
	b = d.reversal[b]
	d.code = b >> (8 - length)
	return 0, true
}

func (d *Decoder) step() (bool, error) {
	if d.decoderState < 0 || d.decoderState >= len(d.stateTable) {
		return false, ErrUnsupportedVideoPacket
	}
	bits := d.stateTable[d.decoderState].bitsToRead
	bit, ok := d.getBit(bits)
	if !ok {
		return false, nil
	}
	d.countBytes++
	if bit == 4 {
		return true, nil
	}
	if d.code == 0 {
		d.nextState = d.stateTable[d.decoderState].state0
	} else {
		d.nextState = d.stateTable[d.decoderState].state1
	}

	switch d.decoderState {
	case 0:
		d.cache.reset()
		d.pixelCount = 0
		d.lastX = 0
		d.lastY = 0
		d.fatalCount = 0
		d.timeoutCount = -1
	case 20:
		d.code = byte(int(d.lastX) + int(d.code) + 1)
		d.lastX = d.code & 0x7f
	case 21:
		d.lastX = d.code
		if d.blockHeight == 16 {
			d.lastX &= 0x7f
		}
	case 17, 26:
		d.newX = d.code
	case 39:
		d.newY = d.code
		if d.blockHeight == 16 {
			d.newY &= 0x7f
		}
		d.lastX = d.newX
		d.lastY = d.newY
	case 35:
		d.nextState = d.cache.pixcode
	case 3, 4, 5, 6, 7, 32:
		if d.cache.active == 1 {
			d.code = byte(d.cache.element[0].usage)
		} else if d.decoderState == 4 {
			d.code = 0
		} else if d.decoderState == 3 {
			d.code = 1
		} else if d.code != 0 {
			d.code++
		}
		if d.cache.find(uint16(d.code), &d.color) != 0 {
			d.nextState = 38
		} else {
			d.storePixel(d.color)
		}
	case 9:
		d.color = uint16(d.code) << (d.bitsPerColor * 2)
	case 41:
		d.color |= uint16(d.code) << d.bitsPerColor
	case 8:
		d.color = uint16(d.code) << (d.bitsPerColor * 2)
		d.color |= uint16(d.code) << d.bitsPerColor
		fallthrough
	case 42:
		d.color = (d.color &^ uint16((1<<d.bitsPerColor)-1)) | uint16(d.code)
		var indx, length, lru uint16
		hit := d.cache.lru(d.color, &indx, &length, &lru)
		d.stateTable[31].state1 = d.cache.pixcode
		if hit != 0 {
			d.nextState = 38
		} else {
			d.storePixel(d.color)
		}
	case 12:
		if d.code == 7 {
			d.nextState = 14
			break
		}
		if d.code == 6 {
			d.nextState = 13
			break
		}
		d.code += 2
		for i := byte(0); i < d.code; i++ {
			d.storePixel(d.lastColor)
		}
	case 13:
		d.code += 8
		fallthrough
	case 14:
		for i := byte(0); i < d.code; i++ {
			d.storePixel(d.lastColor)
		}
	case 33:
		d.storePixel(d.lastColor)
	case 27:
		if d.timeoutCount == int(d.countBytes)-1 {
			d.nextState = 38
		}
		if !d.discardToByteBoundary() {
			d.nextState = d.decoderState
			return false, nil
		}
		if d.countBytes < 1<<31 {
			d.timeoutCount = int(d.countBytes)
		}
	case 24:
		if d.cmdCount != 0 && d.cmdCount-1 < len(d.cmdBuff) {
			d.cmdBuff[d.cmdCount-1] = d.cmdLast
		}
		if d.cmdCount < len(d.cmdBuff) {
			d.cmdCount++
		}
		d.cmdLast = d.code
	case 46:
		if d.code == 0 {
			if !d.processCommand() {
				return false, nil
			}
			d.cmdCount = 0
		}
	case 44:
	case 45:
		if d.code == 0 {
			d.nextState = 1
		}
	case 38:
		d.fatalCount++
		if d.fatalCount == 32768 {
			d.fatalCount = 0
		}
	case 34:
		d.nextBlock(1)
	case 29:
		d.code += 2
		d.nextBlock(uint16(d.code))
	case 30:
		d.nextBlock(uint16(d.code))
	case 40:
		if d.sizeX != d.newX || d.sizeY != d.code {
			d.clearScreen()
		}
		d.sizeX = d.newX
		d.sizeY = d.code
	case 47:
		d.lastX = 0
		d.lastY = 0
		d.pixelCount = 0
		d.cache.reset()
		d.sizeY1 = d.code
		d.switchVideoMode(int(d.sizeX)*d.blockWidth, int(d.sizeY)*16+int(d.sizeY1))
	case 43:
		if d.nextState != d.decoderState {
			d.ibBcnt = 0
			d.ibAcc = 0
			d.zeroCount = 0
			d.countBytes = 0
		}
	case 37:
		d.halt = true
	}

	if d.nextState == 2 && d.pixelCount == uint32(d.blockHeight*d.blockWidth) {
		d.nextBlock(1)
		d.cache.prune()
		d.stateTable[31].state1 = d.cache.pixcode
	}
	if d.decoderState != d.nextState || d.decoderState == 45 || d.decoderState == 38 || d.decoderState == 43 {
		d.decoderState = d.nextState
	}
	return true, nil
}

func (d *Decoder) discardToByteBoundary() bool {
	for d.ibBcnt&7 != 0 {
		if _, ok := d.getBit(1); !ok {
			return false
		}
	}
	return true
}

func (d *Decoder) processCommand() bool {
	switch d.cmdLast {
	case 1:
		d.nextState = 37
	case 6:
		d.clearScreen()
	case 9:
		if d.ibBcnt&7 != 0 {
			return d.discardToByteBoundary()
		}
	case 11:
		if d.cmdCount > 0 {
			d.setBitsPerColor(d.cmdBuff[0])
		}
	case 12:
		if d.cmdCount > 0 {
			d.setEncryption(LegacyCipher(d.cmdBuff[0]))
		}
	case 13:
		d.processHeader(d.cmdBuff[:])
	case 16:
	case 2, 3, 4, 5, 7, 8, 10, 128:
	}
	return true
}

func (d *Decoder) processHeader(cmd []byte) {
	if len(cmd) < 4 {
		return
	}
	d.setBitsPerColor(cmd[0])
	d.setEncryption(LegacyCipher(cmd[1]))
	d.setFlags(cmd[3])
	if !d.readyToWrite {
		d.readyToWrite = true
		d.frameRevision++
	}
}

func (d *Decoder) setEncryption(mode LegacyCipher) {
	d.encryption = mode
	d.encryptionID++
}

func (d *Decoder) setBitsPerColor(bpc byte) {
	d.bitsPerColor = 5 - (uint32(bpc) & 3)
	d.stateTable[8].bitsToRead = d.bitsPerColor
	d.stateTable[9].bitsToRead = d.bitsPerColor
	d.stateTable[41].bitsToRead = d.bitsPerColor
	d.stateTable[42].bitsToRead = d.bitsPerColor
}

func (d *Decoder) setFlags(flags byte) {
	d.halfHeightCapable = flags&8 == 8
}

func (d *Decoder) setHalfHeight() {
	if d.pixelWidth > 1616 && d.halfHeightCapable {
		d.blockHeight = 8
		d.stateTable[21].bitsToRead = 8
		d.stateTable[17].bitsToRead = 8
		d.stateTable[39].bitsToRead = 8
		d.stateTable[30].bitsToRead = 8
		return
	}
	d.blockHeight = 16
	d.stateTable[21].bitsToRead = 7
	d.stateTable[17].bitsToRead = 7
	d.stateTable[39].bitsToRead = 7
	d.stateTable[30].bitsToRead = 7
}

func (d *Decoder) nextBlock(count uint16) {
	d.nextState = 1
	d.pixelCount = 0
	changed := false
	for count != 0 {
		if d.lastX >= d.sizeX || int(d.lastY)*d.blockHeight >= d.pixelHeight {
			count--
			continue
		}
		baseX := int(d.lastX) * d.blockWidth
		baseY := int(d.lastY) * d.blockHeight
		for y := 0; y < d.blockHeight; y++ {
			for x := 0; x < d.blockWidth; x++ {
				idx := y*d.blockWidth + x
				if idx < len(d.block) {
					changed = d.Framebuffer.StorePixel(baseX+x, baseY+y, d.block[idx]) || changed
				}
			}
		}
		d.lastX++
		count--
	}
	if changed {
		d.frameRevision++
	}
}

func (d *Decoder) clearScreen() {
	if d.Framebuffer.Clear(color.RGBA{A: 255}) {
		d.frameRevision++
	}
}

func (d *Decoder) switchVideoMode(cx, cy int) {
	if cx == 0 || cy == 0 {
		cx = 800
		cy = 600
	}
	if cx != d.pixelWidth || cy != d.pixelHeight {
		d.pixelWidth = cx
		d.pixelHeight = cy
		d.Framebuffer = NewFramebuffer(cx, cy)
		if d.Framebuffer.Clear(color.RGBA{A: 255}) {
			d.frameRevision++
		}
	}
	d.setHalfHeight()
}

func (d *Decoder) storePixel(pcolor uint16) {
	if d.pixelCount < uint32(d.blockHeight*d.blockWidth) && int(d.pixelCount) < len(d.block) {
		d.block[d.pixelCount] = d.decodeColor(pcolor)
	}
	d.lastColor = pcolor
	d.pixelCount++
}

func (d *Decoder) decodeColor(pcolor uint16) color.RGBA {
	bits := d.bitsPerColor
	if bits == 0 || bits > 5 {
		return color.RGBA{A: 255}
	}
	mask := uint16((1 << bits) - 1)
	shift := 8 - bits
	r := byte(((pcolor >> (bits * 2)) & mask) << shift)
	g := byte(((pcolor >> bits) & mask) << shift)
	b := byte((pcolor & mask) << shift)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func (d *Decoder) compact() {
	if d.readPtr == 0 {
		return
	}
	if d.readPtr >= len(d.buf) {
		d.buf = d.buf[:0]
		d.readPtr = 0
		return
	}
	copy(d.buf, d.buf[d.readPtr:])
	d.buf = d.buf[:len(d.buf)-d.readPtr]
	d.readPtr = 0
}
