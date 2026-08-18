//go:build windows

package app

import (
	"github.com/lxn/win"
)

func (w *appWindow) handleRawKey(vk, scan uint32, extended, down bool) bool {
	w.updatePointerCapture()
	w.mu.Lock()
	ready := w.inputReady && w.captured
	w.mu.Unlock()
	if !ready {
		return false
	}

	hpVK := normalizeHPVK(vk, scan, extended)
	key := keyForVK(hpVK)
	w.input.Lock()
	if hpVK < 256 {
		w.rawInput = true
		w.rawPressed[byte(hpVK)] = down
	}
	if down {
		w.pressed[key] = true
	} else {
		delete(w.pressed, key)
	}
	w.input.Unlock()
	if down {
		w.logf("raw key down vk=%d hp_vk=%d scan=%d extended=%v key=%d", vk, hpVK, scan, extended, key)
	} else {
		w.logf("raw key up vk=%d hp_vk=%d scan=%d extended=%v key=%d", vk, hpVK, scan, extended, key)
	}
	w.sendKeyboard()
	return true
}

func normalizeHPVK(vk, scan uint32, extended bool) uint32 {
	switch vk {
	case win.VK_SHIFT:
		if scan == 0x36 {
			return win.VK_RSHIFT
		}
		return win.VK_LSHIFT
	case win.VK_CONTROL:
		if extended {
			return win.VK_RCONTROL
		}
		return win.VK_LCONTROL
	case win.VK_MENU:
		if extended {
			return win.VK_RMENU
		}
		return win.VK_LMENU
	case win.VK_DELETE:
		if !extended {
			return win.VK_DECIMAL
		}
	case win.VK_INSERT:
		if !extended {
			return win.VK_NUMPAD0
		}
	case win.VK_END:
		if !extended {
			return win.VK_NUMPAD1
		}
	case win.VK_DOWN:
		if !extended {
			return win.VK_NUMPAD2
		}
	case win.VK_NEXT:
		if !extended {
			return win.VK_NUMPAD3
		}
	case win.VK_LEFT:
		if !extended {
			return win.VK_NUMPAD4
		}
	case win.VK_CLEAR:
		if !extended {
			return win.VK_NUMPAD5
		}
	case win.VK_RIGHT:
		if !extended {
			return win.VK_NUMPAD6
		}
	case win.VK_HOME:
		if !extended {
			return win.VK_NUMPAD7
		}
	case win.VK_UP:
		if !extended {
			return win.VK_NUMPAD8
		}
	case win.VK_PRIOR:
		if !extended {
			return win.VK_NUMPAD9
		}
	}
	return vk
}

func keyForVK(vk uint32) Key {
	switch vk {
	case win.VK_LSHIFT:
		return KeyLShift
	case win.VK_RSHIFT:
		return KeyRShift
	case win.VK_LCONTROL:
		return KeyLControl
	case win.VK_RCONTROL:
		return KeyRControl
	case win.VK_LMENU:
		return KeyLAlt
	case win.VK_RMENU:
		return KeyRAlt
	}
	return Key(vk)
}

func hpKeyboardReport(pressed [256]bool) [10]byte {
	var report [10]byte
	report[0] = 1
	keySlot := 4
	shift := pressed[win.VK_LSHIFT] || pressed[win.VK_RSHIFT]
	for vk, down := range pressed {
		if !down {
			continue
		}
		hid := hpVK409ToHID[vk]
		if shift && vk == win.VK_OEM_MINUS {
			hid = 56
		}
		if hid == 0 || hid == 255 {
			continue
		}
		if hid >= 224 && hid <= 231 {
			report[2] |= 1 << (hid - 224)
			continue
		}
		report[keySlot] = hid
		if keySlot < 9 {
			keySlot++
		}
	}
	return report
}

var hpVK409ToHID = [256]byte{
	3: 72, 8: 42, 9: 43, 13: 40, 19: 72, 20: 57, 27: 41, 32: 44,
	33: 75, 34: 78, 35: 77, 36: 74, 37: 80, 38: 82, 39: 79, 40: 81,
	44: 70, 45: 73, 46: 76, 48: 39, 49: 30, 50: 31, 51: 32, 52: 33,
	53: 34, 54: 35, 55: 36, 56: 37, 57: 38,
	65: 4, 66: 5, 67: 6, 68: 7, 69: 8, 70: 9, 71: 10, 72: 11,
	73: 12, 74: 13, 75: 14, 76: 15, 77: 16, 78: 17, 79: 18, 80: 19,
	81: 20, 82: 21, 83: 22, 84: 23, 85: 24, 86: 25, 87: 26, 88: 27,
	89: 29, 90: 28, 91: 227, 92: 231, 93: 101,
	96: 98, 97: 89, 98: 90, 99: 91, 100: 92, 101: 93, 102: 94,
	103: 95, 104: 96, 105: 97, 106: 85, 107: 87, 109: 86, 110: 99,
	111: 84, 112: 58, 113: 59, 114: 60, 115: 61, 116: 62, 117: 63,
	118: 64, 119: 65, 120: 66, 121: 67, 122: 68, 123: 69,
	144: 83, 145: 71, 160: 225, 161: 229, 162: 224, 163: 228,
	164: 226, 165: 230, 186: 51, 187: 46, 188: 54, 189: 45, 190: 55,
	191: 56, 192: 53, 219: 47, 220: 49, 221: 48, 222: 52, 226: 100,
	255: 255,
}
