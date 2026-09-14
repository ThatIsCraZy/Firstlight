package rfb

import "testing"

func TestKeysymForHIDCoversLettersAndSymbols(t *testing.T) {
	cases := []struct {
		name   string
		usage  byte
		shift  bool
		keysym uint32
	}{
		{"lowercase a", 4, false, 'a'},
		{"uppercase A", 4, true, 'A'},
		{"digit 1", 30, false, '1'},
		{"exclamation mark", 30, true, '!'},
		{"digit 0", 39, false, '0'},
		{"closing parenthesis", 39, true, ')'},
		{"minus", 45, false, '-'},
		{"underscore", 45, true, '_'},
		{"return", 40, false, 0xff0d},
		{"return ignores shift", 40, true, 0xff0d},
		{"space", 44, false, 0x20},
		{"F1", 58, false, 0xffbe},
		{"F12", 69, false, 0xffc9},
		{"delete", 76, false, 0xffff},
		{"arrow up", 82, false, 0xff52},
		{"keypad 0", 98, false, 0xffb0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := KeysymForHID(tc.usage, tc.shift)
			if !ok {
				t.Fatalf("usage %d has no mapping", tc.usage)
			}
			if got != tc.keysym {
				t.Fatalf("keysym = %#x, want %#x", got, tc.keysym)
			}
		})
	}
}

func TestKeysymForHIDReportsUnknownUsage(t *testing.T) {
	if _, ok := KeysymForHID(200, false); ok {
		t.Fatal("usage 200 should have no mapping")
	}
}

func TestKeysymForRune(t *testing.T) {
	cases := []struct {
		in   rune
		want uint32
	}{
		{'A', 'A'},
		{'z', 'z'},
		{'ä', 0xe4},       // Latin-1 keeps its code point
		{'€', 0x010020ac}, // beyond Latin-1 uses the Unicode escape
		{'\n', 0xff0d},
		{'\t', 0xff09},
	}
	for _, tc := range cases {
		if got := KeysymForRune(tc.in); got != tc.want {
			t.Fatalf("KeysymForRune(%q) = %#x, want %#x", tc.in, got, tc.want)
		}
	}
}

// report builds a HID keyboard report the way kvm.KeyboardReport does.
func report(modifiers byte, keys ...byte) [10]byte {
	var out [10]byte
	out[0] = 1
	out[2] = modifiers
	for i, key := range keys {
		if i >= 6 {
			break
		}
		out[4+i] = key
	}
	return out
}

func TestKeyboardStateEmitsPressAndRelease(t *testing.T) {
	var state KeyboardState
	events := state.Apply(report(0, 4)) // press 'a'
	if len(events) != 1 || !events[0].Down || events[0].Keysym != 'a' {
		t.Fatalf("press events = %+v", events)
	}
	events = state.Apply(report(0)) // release
	if len(events) != 1 || events[0].Down || events[0].Keysym != 'a' {
		t.Fatalf("release events = %+v", events)
	}
	if state.Held() {
		t.Fatal("no key should be held after the release")
	}
}

// A modifier has to go down before the key it shifts, otherwise the server
// sees the unshifted character.
func TestKeyboardStateSendsModifierBeforeKey(t *testing.T) {
	var state KeyboardState
	events := state.Apply(report(ModLeftShift, 4)) // Shift + 'a'
	if len(events) != 2 {
		t.Fatalf("events = %+v, want two", events)
	}
	if events[0].Keysym != KeyShiftL || !events[0].Down {
		t.Fatalf("first event = %+v, want Shift_L down", events[0])
	}
	if events[1].Keysym != 'A' || !events[1].Down {
		t.Fatalf("second event = %+v, want 'A' down", events[1])
	}
}

// Releasing everything at once must release the key before the modifier, so
// the remote side never observes a lone modifier with the key still down.
func TestKeyboardStateReleasesKeysBeforeModifiers(t *testing.T) {
	var state KeyboardState
	state.Apply(report(ModLeftCtrl, 4))
	events := state.Apply(report(0))
	if len(events) != 2 {
		t.Fatalf("events = %+v, want two", events)
	}
	if events[0].Down || events[0].Keysym != 'a' {
		t.Fatalf("first event = %+v, want 'a' up", events[0])
	}
	if events[1].Down || events[1].Keysym != KeyControlL {
		t.Fatalf("second event = %+v, want Control_L up", events[1])
	}
}

func TestKeyboardStateHandlesCtrlAltDelete(t *testing.T) {
	var state KeyboardState
	events := state.Apply(report(ModLeftCtrl|ModLeftAlt, 76))
	if len(events) != 3 {
		t.Fatalf("events = %+v, want three", events)
	}
	var sawCtrl, sawAlt, sawDelete bool
	for _, event := range events {
		if !event.Down {
			t.Fatalf("unexpected release in %+v", events)
		}
		switch event.Keysym {
		case KeyControlL:
			sawCtrl = true
		case KeyAltL:
			sawAlt = true
		case 0xffff:
			sawDelete = true
		}
	}
	if !sawCtrl || !sawAlt || !sawDelete {
		t.Fatalf("events = %+v, want Control, Alt and Delete", events)
	}
}

// Holding one key while another is added must not re-send the held one.
func TestKeyboardStateSendsOnlyTheDifference(t *testing.T) {
	var state KeyboardState
	state.Apply(report(0, 4))
	events := state.Apply(report(0, 4, 5))
	if len(events) != 1 || events[0].Keysym != 'b' || !events[0].Down {
		t.Fatalf("events = %+v, want only 'b' down", events)
	}
}
