//go:build windows

package app

import (
	"testing"

	"firstlight/internal/kvm"
)

func TestFunctionKeysMapToTheirHIDUsages(t *testing.T) {
	for vk, want := range map[Key]byte{
		KeyF1: 58, KeyF2: 59, KeyF3: 60, KeyF4: 61, KeyF5: 62, KeyF6: 63,
		KeyF7: 64, KeyF8: 65, KeyF9: 66, KeyF10: 67, KeyF11: 68, KeyF12: 69,
	} {
		var raw [256]bool
		raw[byte(vk)] = true
		if got, expect := hpKeyboardReport(raw), kvm.KeyboardReport(0, want); got != expect {
			t.Errorf("default path vk=0x%02X: got %v want %v", uint16(vk), got, expect)
		}

		pressed := map[Key]bool{vk: true}
		if got, expect := keyboardReportForLayout(keyboardLayoutDefault, pressed), kvm.KeyboardReport(0, want); got != expect {
			t.Errorf("mapped default vk=0x%02X: got %v want %v", uint16(vk), got, expect)
		}
		if got, expect := keyboardReportForLayout("german", pressed), kvm.KeyboardReport(0, want); got != expect {
			t.Errorf("german vk=0x%02X: got %v want %v", uint16(vk), got, expect)
		}
	}
}

func TestFunctionKeysSurviveVKNormalization(t *testing.T) {
	for vk := uint32(0x70); vk <= 0x7B; vk++ {
		if got := normalizeHPVK(vk, 0x3C, false); got != vk {
			t.Errorf("normalizeHPVK(0x%02X) = 0x%02X", vk, got)
		}
		if got := keyForVK(vk); uint32(got) != vk {
			t.Errorf("keyForVK(0x%02X) = 0x%02X", vk, uint32(got))
		}
	}
}
