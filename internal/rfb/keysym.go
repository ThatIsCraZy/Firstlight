package rfb

// RFB transmits X11 keysyms, while Firstlight's keyboard pipeline and its
// layout files speak USB HID usage codes. This file bridges the two so the
// existing layout system, chord parser and clipboard typist keep working
// against a BMC that speaks RFB.

// Modifier bits of a HID keyboard report, in the order the USB HID
// specification defines them.
const (
	ModLeftCtrl   = 1 << 0
	ModLeftShift  = 1 << 1
	ModLeftAlt    = 1 << 2
	ModLeftGUI    = 1 << 3
	ModRightCtrl  = 1 << 4
	ModRightShift = 1 << 5
	ModRightAlt   = 1 << 6
	ModRightGUI   = 1 << 7
)

// Keysyms for the modifier keys themselves.
const (
	KeyShiftL   = 0xffe1
	KeyShiftR   = 0xffe2
	KeyControlL = 0xffe3
	KeyControlR = 0xffe4
	KeyAltL     = 0xffe9
	KeyAltR     = 0xffea
	KeySuperL   = 0xffeb
	KeySuperR   = 0xffec
)

// modifierKeysyms pairs each report bit with the keysym it stands for.
var modifierKeysyms = []struct {
	bit    byte
	keysym uint32
}{
	{ModLeftCtrl, KeyControlL},
	{ModLeftShift, KeyShiftL},
	{ModLeftAlt, KeyAltL},
	{ModLeftGUI, KeySuperL},
	{ModRightCtrl, KeyControlR},
	{ModRightShift, KeyShiftR},
	{ModRightAlt, KeyAltR},
	{ModRightGUI, KeySuperR},
}

// hidBase maps a HID usage code to the keysym produced without shift.
var hidBase = buildBase()

// hidShifted holds the entries that change under shift. Anything missing
// falls back to hidBase.
var hidShifted = buildShifted()

func buildBase() map[byte]uint32 {
	table := map[byte]uint32{
		40: 0xff0d, // Return
		41: 0xff1b, // Escape
		42: 0xff08, // BackSpace
		43: 0xff09, // Tab
		44: 0x0020, // space
		45: '-',
		46: '=',
		47: '[',
		48: ']',
		49: '\\',
		50: '#', // non-US number sign
		51: ';',
		52: '\'',
		53: '`',
		54: ',',
		55: '.',
		56: '/',
		57: 0xffe5, // Caps_Lock
		70: 0xff61, // Print
		71: 0xff14, // Scroll_Lock
		72: 0xff13, // Pause
		73: 0xff63, // Insert
		74: 0xff50, // Home
		75: 0xff55, // Page_Up
		76: 0xffff, // Delete
		77: 0xff57, // End
		78: 0xff56, // Page_Down
		79: 0xff53, // Right
		80: 0xff51, // Left
		81: 0xff54, // Down
		82: 0xff52, // Up
		83: 0xff7f, // Num_Lock
		84: 0xffaf, // KP_Divide
		85: 0xffaa, // KP_Multiply
		86: 0xffad, // KP_Subtract
		87: 0xffab, // KP_Add
		88: 0xff8d, // KP_Enter
		99: 0xffae, // KP_Decimal

		100: '\\',   // non-US backslash
		101: 0xff67, // Menu
		104: 0xffc9, // F13 and above are rare but cheap to carry
	}
	// a to z
	for i := byte(0); i < 26; i++ {
		table[4+i] = uint32('a' + i)
	}
	// 1 to 9 then 0
	digits := "1234567890"
	for i := 0; i < len(digits); i++ {
		table[byte(30+i)] = uint32(digits[i])
	}
	// F1 to F12
	for i := byte(0); i < 12; i++ {
		table[58+i] = 0xffbe + uint32(i)
	}
	// Keypad 1 to 9 then 0
	for i := byte(0); i < 9; i++ {
		table[89+i] = 0xffb1 + uint32(i)
	}
	table[98] = 0xffb0
	return table
}

func buildShifted() map[byte]uint32 {
	table := map[byte]uint32{
		45:  '_',
		46:  '+',
		47:  '{',
		48:  '}',
		49:  '|',
		50:  '~',
		51:  ':',
		52:  '"',
		53:  '~',
		54:  '<',
		55:  '>',
		56:  '?',
		100: '|',
	}
	// A to Z
	for i := byte(0); i < 26; i++ {
		table[4+i] = uint32('A' + i)
	}
	// The US symbols above the digit row.
	symbols := "!@#$%^&*()"
	for i := 0; i < len(symbols); i++ {
		table[byte(30+i)] = uint32(symbols[i])
	}
	return table
}

// KeysymForHID returns the keysym a HID usage code produces, taking shift into
// account. The second result is false when the code has no mapping.
func KeysymForHID(usage byte, shift bool) (uint32, bool) {
	if shift {
		if keysym, ok := hidShifted[usage]; ok {
			return keysym, true
		}
	}
	keysym, ok := hidBase[usage]
	return keysym, ok
}

// KeysymForRune maps a character directly, which is what clipboard typing
// needs. Latin-1 is its own keysym range; everything else uses the Unicode
// escape defined by the X Keyboard Extension.
func KeysymForRune(r rune) uint32 {
	switch r {
	case '\n', '\r':
		return 0xff0d
	case '\t':
		return 0xff09
	case '\b':
		return 0xff08
	}
	if r >= 0x20 && r <= 0xff {
		return uint32(r)
	}
	return 0x01000000 + uint32(r)
}

// KeyEvent is one keysym transition.
type KeyEvent struct {
	Keysym uint32
	Down   bool
}

// KeyboardState turns the stream of full HID reports that Firstlight's input
// layer produces into the down and up events RFB expects. It remembers the
// previous report so only the difference goes on the wire.
type KeyboardState struct {
	modifiers byte
	keys      []byte
}

// Apply diffs a HID report against the previous one. The report layout matches
// kvm.KeyboardReport: byte 2 holds the modifiers, bytes 4 to 9 the usage codes.
func (k *KeyboardState) Apply(report [10]byte) []KeyEvent {
	modifiers := report[2]
	var keys []byte
	for _, usage := range report[4:10] {
		if usage != 0 {
			keys = append(keys, usage)
		}
	}
	var events []KeyEvent

	// Releases first, so a chord never leaves a stale key held down.
	for _, usage := range k.keys {
		if !containsByte(keys, usage) {
			if keysym, ok := KeysymForHID(usage, k.modifiers&(ModLeftShift|ModRightShift) != 0); ok {
				events = append(events, KeyEvent{Keysym: keysym, Down: false})
			}
		}
	}
	for _, modifier := range modifierKeysyms {
		if k.modifiers&modifier.bit != 0 && modifiers&modifier.bit == 0 {
			events = append(events, KeyEvent{Keysym: modifier.keysym, Down: false})
		}
	}
	// Then presses, modifiers before the keys they alter.
	for _, modifier := range modifierKeysyms {
		if k.modifiers&modifier.bit == 0 && modifiers&modifier.bit != 0 {
			events = append(events, KeyEvent{Keysym: modifier.keysym, Down: true})
		}
	}
	shift := modifiers&(ModLeftShift|ModRightShift) != 0
	for _, usage := range keys {
		if !containsByte(k.keys, usage) {
			if keysym, ok := KeysymForHID(usage, shift); ok {
				events = append(events, KeyEvent{Keysym: keysym, Down: true})
			}
		}
	}
	k.modifiers = modifiers
	k.keys = keys
	return events
}

// Reset forgets the remembered report without emitting events. Callers that
// send an explicit all-keys-up report should use Apply instead.
func (k *KeyboardState) Reset() {
	k.modifiers = 0
	k.keys = nil
}

// Held reports whether any key or modifier is currently down.
func (k *KeyboardState) Held() bool {
	return k.modifiers != 0 || len(k.keys) > 0
}

func containsByte(haystack []byte, needle byte) bool {
	for _, value := range haystack {
		if value == needle {
			return true
		}
	}
	return false
}
