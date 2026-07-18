//go:build windows

package app

import (
	"testing"

	"github.com/lxn/walk"

	"hpeirc/internal/kvm"
)

func TestKeyboardLayoutDefaultUsesPhysicalYZ(t *testing.T) {
	tests := []struct {
		name string
		key  walk.Key
		hid  byte
	}{
		{name: "Y key sends physical Z position", key: walk.KeyY, hid: 29},
		{name: "Z key sends physical Y position", key: walk.KeyZ, hid: 28},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutDefault, keys(tt.key))
			want := kvm.KeyboardReport(0, tt.hid)
			if got != want {
				t.Fatalf("report=%x want=%x", got, want)
			}
		})
	}
}

func TestKeyboardLayoutDefaultRawUsesPhysicalYZ(t *testing.T) {
	var yPressed [256]bool
	yPressed['Y'] = true
	if got, want := hpKeyboardReport(yPressed), kvm.KeyboardReport(0, 29); got != want {
		t.Fatalf("raw Y report=%x want=%x", got, want)
	}

	var zPressed [256]bool
	zPressed['Z'] = true
	if got, want := hpKeyboardReport(zPressed), kvm.KeyboardReport(0, 28); got != want {
		t.Fatalf("raw Z report=%x want=%x", got, want)
	}
}

func TestKeyboardLayoutDefaultKeepsShiftOEMMinus(t *testing.T) {
	got := keyboardReportForLayout(keyboardLayoutDefault, keys(walk.KeyShift, walk.KeyOEMMinus))
	want := kvm.KeyboardReport(2, 56)
	if got != want {
		t.Fatalf("default shift OEMMinus report=%x want=%x", got, want)
	}
}

func TestKeyboardLayoutDefaultRawKeepsShiftOEMMinus(t *testing.T) {
	var pressed [256]bool
	pressed[160] = true
	pressed[189] = true
	got := hpKeyboardReport(pressed)
	want := kvm.KeyboardReport(2, 56)
	if got != want {
		t.Fatalf("default raw shift OEMMinus report=%x want=%x", got, want)
	}
}

func TestKeyboardLayoutForceGermanLeavesLettersUnchanged(t *testing.T) {
	tests := []struct {
		name string
		key  walk.Key
		hid  byte
	}{
		{name: "Y", key: walk.KeyY, hid: 28},
		{name: "Z", key: walk.KeyZ, hid: 29},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(tt.key))
			want := kvm.KeyboardReport(0, tt.hid)
			if got != want {
				t.Fatalf("report=%x want=%x", got, want)
			}
		})
	}
}

func TestKeyboardLayoutForceGermanShiftSymbols(t *testing.T) {
	tests := []struct {
		name string
		key  walk.Key
		want [10]byte
	}{
		{name: "quote", key: walk.Key2, want: kvm.KeyboardReport(2, 52)},
		{name: "slash", key: walk.Key7, want: kvm.KeyboardReport(0, 56)},
		{name: "paren-left", key: walk.Key8, want: kvm.KeyboardReport(2, 38)},
		{name: "underscore", key: walk.KeyOEMMinus, want: kvm.KeyboardReport(2, 45)},
		{name: "asterisk", key: walk.KeyOEMPlus, want: kvm.KeyboardReport(2, 37)},
		{name: "question", key: walk.KeyOEM4, want: kvm.KeyboardReport(2, 56)},
		{name: "backtick", key: walk.KeyOEM6, want: kvm.KeyboardReport(0, 53)},
		{name: "apostrophe", key: walk.KeyOEM2, want: kvm.KeyboardReport(0, 52)},
		{name: "colon", key: walk.KeyOEMPeriod, want: kvm.KeyboardReport(2, 51)},
		{name: "greater-than", key: walk.KeyOEM102, want: kvm.KeyboardReport(2, 55)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(walk.KeyShift, tt.key))
			if got != tt.want {
				t.Fatalf("report=%x want=%x", got, tt.want)
			}
		})
	}
}

func TestKeyboardLayoutForceGermanPlainSymbols(t *testing.T) {
	tests := []struct {
		name string
		key  walk.Key
		want [10]byte
	}{
		{name: "less-than", key: walk.KeyOEM102, want: kvm.KeyboardReport(2, 54)},
		{name: "hash", key: walk.KeyOEM2, want: kvm.KeyboardReport(2, 32)},
		{name: "plus", key: walk.KeyOEMPlus, want: kvm.KeyboardReport(2, 46)},
		{name: "plain-ss-unsupported", key: walk.KeyOEM4, want: kvm.KeyboardReport(0)},
		{name: "plain-acute-unsupported", key: walk.KeyOEM6, want: kvm.KeyboardReport(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(tt.key))
			if got != tt.want {
				t.Fatalf("report=%x want=%x", got, tt.want)
			}
		})
	}
}

func TestKeyboardLayoutForceGermanAltGrSymbols(t *testing.T) {
	tests := []struct {
		name string
		key  walk.Key
		want [10]byte
	}{
		{name: "at", key: walk.KeyQ, want: kvm.KeyboardReport(2, 31)},
		{name: "brace-left", key: walk.Key7, want: kvm.KeyboardReport(2, 47)},
		{name: "bracket-left", key: walk.Key8, want: kvm.KeyboardReport(0, 47)},
		{name: "tilde", key: walk.KeyOEMPlus, want: kvm.KeyboardReport(2, 53)},
		{name: "backslash-ss", key: walk.KeyOEM4, want: kvm.KeyboardReport(0, 49)},
		{name: "backslash", key: walk.KeyOEMMinus, want: kvm.KeyboardReport(0, 49)},
		{name: "pipe", key: walk.KeyOEM102, want: kvm.KeyboardReport(2, 49)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(walk.KeyRAlt, tt.key))
			if got != tt.want {
				t.Fatalf("report=%x want=%x", got, tt.want)
			}
		})
	}
}

func TestKeyboardLayoutDefaultAltGrSymbols(t *testing.T) {
	got := keyboardReportForLayout(keyboardLayoutDefault, keys(walk.KeyRAlt, walk.KeyQ))
	want := kvm.KeyboardReport(2, 31)
	if got != want {
		t.Fatalf("default altgr+q report=%x want=%x", got, want)
	}
}

func TestKeyboardLayoutAltGrWhenWindowsReportsCtrlAlt(t *testing.T) {
	got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(walk.KeyControl, walk.KeyAlt, walk.KeyQ))
	want := kvm.KeyboardReport(2, 31)
	if got != want {
		t.Fatalf("ctrl+alt+q altgr report=%x want=%x", got, want)
	}
}

func TestKeyboardLayoutModifierOnlyReportsAllKeysUp(t *testing.T) {
	tests := []struct {
		name string
		keys []walk.Key
	}{
		{name: "right alt only", keys: []walk.Key{walk.KeyRAlt}},
		{name: "ctrl alt only", keys: []walk.Key{walk.KeyControl, walk.KeyAlt}},
		{name: "shift only", keys: []walk.Key{walk.KeyShift}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyboardReportForLayout(keyboardLayoutDefault, keys(tt.keys...))
			want := kvm.KeyboardReport(0)
			if got != want {
				t.Fatalf("report=%x want=%x", got, want)
			}
		})
	}
}

func TestKeyboardLayoutAltGrReleaseClearsRemoteModifiers(t *testing.T) {
	pressed := keys(walk.KeyRAlt, walk.KeyQ)
	down := keyboardReportForLayout(keyboardLayoutDefault, pressed)
	if down != kvm.KeyboardReport(2, 31) {
		t.Fatalf("altgr+q down report=%x", down)
	}
	delete(pressed, walk.KeyQ)
	up := keyboardReportForLayout(keyboardLayoutDefault, pressed)
	if up != kvm.KeyboardReport(0) {
		t.Fatalf("altgr after q release report=%x want all-up", up)
	}
}

func TestKeyboardLayoutForceGermanPreservesCtrlShortcut(t *testing.T) {
	got := keyboardReportForLayout(keyboardLayoutForceGerman, keys(walk.KeyControl, walk.KeyC))
	want := kvm.KeyboardReport(1, 6)
	if got != want {
		t.Fatalf("ctrl+c report=%x want=%x", got, want)
	}
}

func TestClipboardReportForRune(t *testing.T) {
	tests := []struct {
		name   string
		layout keyboardLayout
		r      rune
		want   [10]byte
	}{
		{name: "letter", layout: keyboardLayoutDefault, r: 'y', want: kvm.KeyboardReport(0, 28)},
		{name: "capital", layout: keyboardLayoutDefault, r: 'Y', want: kvm.KeyboardReport(2, 28)},
		{name: "at-force-german", layout: keyboardLayoutForceGerman, r: '@', want: kvm.KeyboardReport(2, 31)},
		{name: "backslash-force-german", layout: keyboardLayoutForceGerman, r: '\\', want: kvm.KeyboardReport(0, 49)},
		{name: "newline", layout: keyboardLayoutForceGerman, r: '\n', want: kvm.KeyboardReport(0, 40)},
		{name: "less-than", layout: keyboardLayoutForceGerman, r: '<', want: kvm.KeyboardReport(2, 54)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clipboardReportForRune(tt.layout, tt.r)
			if !ok {
				t.Fatal("expected supported rune")
			}
			if got != tt.want {
				t.Fatalf("report=%x want=%x", got, tt.want)
			}
		})
	}
}

func TestClipboardReportForRuneUnsupported(t *testing.T) {
	if _, ok := clipboardReportForRune(keyboardLayoutForceGerman, 'ä'); ok {
		t.Fatal("expected umlaut to be unsupported")
	}
}

func keys(values ...walk.Key) map[walk.Key]bool {
	out := make(map[walk.Key]bool, len(values))
	for _, key := range values {
		out[key] = true
	}
	return out
}
