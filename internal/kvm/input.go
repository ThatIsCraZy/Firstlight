package kvm

import (
	"math"
	"time"
)

type PowerOption byte

const (
	PowerMomentaryPress PowerOption = 0
	PowerPressAndHold   PowerOption = 1
	PowerColdBoot       PowerOption = 2
	PowerReset          PowerOption = 3
)

func (c *Conn) SendPower(option PowerOption) error {
	_, err := c.Write([]byte{0, 0, byte(option), 0})
	return err
}

func (c *Conn) SendAllKeysUp() error {
	return c.SendKeyboardReport(KeyboardReport(0))
}

func (c *Conn) SendCtrlAltDel() error {
	if _, err := c.Write([]byte{1, 0, 5, 0, 76, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	if _, err := c.Write([]byte{1, 0, 5, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return c.SendAllKeysUp()
}

func (c *Conn) SendKeyboardReport(report [10]byte) error {
	_, err := c.Write(report[:])
	return err
}

func KeyboardReport(modifier byte, keys ...byte) [10]byte {
	var report [10]byte
	report[0] = 1
	report[2] = modifier
	for i, key := range keys {
		if i >= 6 {
			break
		}
		report[4+i] = key
	}
	return report
}

func (c *Conn) SendMouse(absX, absY, relX, relY, width, height int, wheel int8, buttons byte) error {
	report := MouseReport(absX, absY, relX, relY, width, height, wheel, buttons)
	_, err := c.Write(report[:])
	return err
}

func MouseReport(absX, absY, relX, relY, width, height int, wheel int8, buttons byte) [10]byte {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	absX = clamp(absX, 0, width)
	absY = clamp(absY, 0, height)
	x := uint16(math.Round(3000 * float64(absX) / float64(width)))
	y := uint16(math.Round(3000 * float64(absY) / float64(height)))
	return [10]byte{
		2, 0,
		byte(x & 0xff), byte(x >> 8),
		byte(y & 0xff), byte(y >> 8),
		relativeByte(relX), relativeByte(relY),
		buttons,
		byte(wheel),
	}
}

func relativeByte(v int) byte {
	v = clamp(v, -127, 127)
	if v < 0 {
		return byte(-v) + 0x80
	}
	return byte(v)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
