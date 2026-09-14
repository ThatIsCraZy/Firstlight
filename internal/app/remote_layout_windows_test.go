//go:build windows

package app

import (
	"context"
	"testing"

	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
)

// HID usages the tests refer to by name, so a failure says which key position
// the report actually carried.
const (
	usageMinus        = 45 // right of the zero: - on US, sharp s on German
	usageSlash        = 56 // right of the period: / on US, - on German
	usageLeftBracket  = 47 // [ on US, u umlaut on German
	usageSemicolon    = 51 // ; on US, o umlaut on German
	usageY            = 28 // y on US, z on German
	usageZ            = 29 // z on US, y on German
	usageComma        = 54
	usageDigit7       = 36
	usageQ            = 20
	usageNonUSBacksl  = 100
	modifierShiftBit  = 1 << 1
	modifierCtrlBit   = 1 << 0
	modifierRightAlt  = 1 << 6
	firstKeySlotIndex = 4
)

func germanTarget(t *testing.T) *keyboardmap.Target {
	t.Helper()
	target, ok := keyboardmap.TargetByID("de-DE")
	if !ok {
		t.Fatal("the German remote layout is missing")
	}
	return target
}

func reportFor(layout keyboardLayout, target *keyboardmap.Target, pressed map[Key]bool) [10]byte {
	return keyboardReportForRegistry(keyboardmap.BuiltInRegistry(), layout, target, pressed)
}

// The reported bug: the key labelled minus produced the sharp s on a German
// Windows, because the report carried the US position of the minus sign and the
// remote layout reads that position as the sharp s.
func TestGermanRemoteLayoutPutsMinusOnTheGermanPosition(t *testing.T) {
	target := germanTarget(t)
	cases := []struct {
		name    string
		layout  keyboardLayout
		pressed map[Key]bool
		usage   byte
		mod     byte
	}{
		{"german map", keyboardLayoutForceGerman, keys(KeyOEMMinus), usageSlash, 0},
		{"default layout", keyboardLayoutDefault, keys(KeyOEMMinus), usageSlash, 0},
		{"shifted, the underscore", keyboardLayoutForceGerman, keys(KeyOEMMinus, KeyLShift), usageSlash, modifierShiftBit},
		{"the sharp s itself", keyboardLayoutForceGerman, keys(KeyOEM4), usageMinus, 0},
		{"u umlaut", keyboardLayoutForceGerman, keys(KeyOEM1), usageLeftBracket, 0},
		{"o umlaut", keyboardLayoutForceGerman, keys(KeyOEM3), usageSemicolon, 0},
		{"the slash, shift and seven", keyboardLayoutForceGerman, keys(Key('7'), KeyLShift), usageDigit7, modifierShiftBit},
		{"the less-than key", keyboardLayoutForceGerman, keys(KeyOEM102), usageNonUSBacksl, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := reportFor(tc.layout, target, tc.pressed)
			if report[firstKeySlotIndex] != tc.usage {
				t.Fatalf("usage = %d, want %d", report[firstKeySlotIndex], tc.usage)
			}
			if report[2] != tc.mod {
				t.Fatalf("modifiers = %02x, want %02x", report[2], tc.mod)
			}
		})
	}
}

// The y and z keys trade places between the two layouts, which makes them the
// clearest check that the translation runs on characters and not on positions.
func TestGermanRemoteLayoutSwapsYAndZ(t *testing.T) {
	target := germanTarget(t)
	if report := reportFor(keyboardLayoutForceGerman, target, keys(KeyY)); report[firstKeySlotIndex] != usageZ {
		t.Fatalf("the key that types y landed on usage %d, want %d", report[firstKeySlotIndex], usageZ)
	}
	if report := reportFor(keyboardLayoutForceGerman, target, keys(KeyZ)); report[firstKeySlotIndex] != usageY {
		t.Fatalf("the key that types z landed on usage %d, want %d", report[firstKeySlotIndex], usageY)
	}
}

// A chord keeps its modifier and still lands on the position the remote layout
// puts the character on.
func TestGermanRemoteLayoutKeepsControlChords(t *testing.T) {
	target := germanTarget(t)
	report := reportFor(keyboardLayoutForceGerman, target, keys(KeyZ, KeyLControl))
	if report[2] != modifierCtrlBit {
		t.Fatalf("modifiers = %02x, want the control bit alone", report[2])
	}
	if report[firstKeySlotIndex] != usageY {
		t.Fatalf("usage = %d, want %d", report[firstKeySlotIndex], usageY)
	}
}

// AltGr characters exist only on the German side. The at sign travels as AltGr
// and the Q position rather than as shift and the digit two.
func TestGermanRemoteLayoutEncodesAltGr(t *testing.T) {
	target := germanTarget(t)
	report := reportFor(keyboardLayoutForceGerman, target, keys(Key('Q'), KeyRAlt))
	if report[firstKeySlotIndex] != usageQ {
		t.Fatalf("usage = %d, want %d", report[firstKeySlotIndex], usageQ)
	}
	if report[2] != modifierRightAlt {
		t.Fatalf("modifiers = %02x, want the right alt bit alone", report[2])
	}
}

// Keys that carry no character are about position, not language. They have to
// pass through the translation untouched.
func TestGermanRemoteLayoutLeavesNonCharacterKeysAlone(t *testing.T) {
	target := germanTarget(t)
	plain := reportFor(keyboardLayoutForceGerman, keyboardmap.DefaultTarget(), keys(KeyF1))
	translated := reportFor(keyboardLayoutForceGerman, target, keys(KeyF1))
	if plain != translated {
		t.Fatalf("F1 changed under the German remote layout: %x versus %x", plain, translated)
	}
}

// The US target is the default and has to leave every report exactly as it was
// before remote layouts existed.
func TestDefaultRemoteLayoutChangesNothing(t *testing.T) {
	for _, layout := range []keyboardLayout{keyboardLayoutDefault, keyboardLayoutForceGerman} {
		for _, pressed := range []map[Key]bool{
			keys(KeyOEMMinus),
			keys(KeyOEMMinus, KeyLShift),
			keys(KeyY),
			keys(KeyZ, KeyLControl),
			keys(Key('7'), KeyLShift),
		} {
			want := keyboardReportForLayout(layout, pressed)
			got := reportFor(layout, keyboardmap.DefaultTarget(), pressed)
			if want != got {
				t.Fatalf("layout %q changed: %x versus %x", layout, want, got)
			}
		}
	}
}

// Clipboard text is characters, so the remote layout alone decides which keys
// produce them. The local map has no say.
func TestClipboardFollowsTheRemoteLayout(t *testing.T) {
	target := germanTarget(t)
	sender := &recordingKeyboardSender{}
	sent, skipped, err := sendClipboardTextWithDelay(
		context.Background(), sender, keyboardmap.BuiltInRegistry(), keyboardLayoutDefault, target, "-ü", 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || skipped != 0 {
		t.Fatalf("sent=%d skipped=%d, want 2 and 0", sent, skipped)
	}
	want := [][10]byte{
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(0, usageSlash),
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(0, usageLeftBracket),
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(0),
	}
	if len(sender.reports) != len(want) {
		t.Fatalf("sent %d reports, want %d: %x", len(sender.reports), len(want), sender.reports)
	}
	for i := range want {
		if sender.reports[i] != want[i] {
			t.Fatalf("report %d = %x, want %x", i, sender.reports[i], want[i])
		}
	}
}

// A character the remote layout cannot produce is counted, not guessed at.
func TestClipboardSkipsWhatTheRemoteLayoutCannotType(t *testing.T) {
	target := germanTarget(t)
	sender := &recordingKeyboardSender{}
	sent, skipped, err := sendClipboardTextWithDelay(
		context.Background(), sender, keyboardmap.BuiltInRegistry(), keyboardLayoutDefault, target, "a中", 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || skipped != 1 {
		t.Fatalf("sent=%d skipped=%d, want 1 and 1", sent, skipped)
	}
}

func TestUnknownRemoteLayoutIsRejected(t *testing.T) {
	if _, ok := keyboardmap.TargetByID("fr-FR"); ok {
		t.Fatal("an unknown remote layout was accepted")
	}
	if target, ok := keyboardmap.TargetByID(""); !ok || !target.IsDefault() {
		t.Fatal("an empty remote layout has to mean the US default")
	}
}
