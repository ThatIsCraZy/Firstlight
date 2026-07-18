//go:build windows

package app

import (
	"testing"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func TestNormalizeHPVKDistinguishesModifiersAndNumpadNavigation(t *testing.T) {
	tests := []struct {
		name     string
		vk       uint32
		scan     uint32
		extended bool
		want     uint32
	}{
		{name: "left shift", vk: win.VK_SHIFT, scan: 0x2a, want: win.VK_LSHIFT},
		{name: "right shift", vk: win.VK_SHIFT, scan: 0x36, want: win.VK_RSHIFT},
		{name: "left control", vk: win.VK_CONTROL, want: win.VK_LCONTROL},
		{name: "right control", vk: win.VK_CONTROL, extended: true, want: win.VK_RCONTROL},
		{name: "left alt", vk: win.VK_MENU, want: win.VK_LMENU},
		{name: "right alt", vk: win.VK_MENU, extended: true, want: win.VK_RMENU},
		{name: "numpad decimal", vk: win.VK_DELETE, want: win.VK_DECIMAL},
		{name: "extended delete", vk: win.VK_DELETE, extended: true, want: win.VK_DELETE},
		{name: "numpad zero", vk: win.VK_INSERT, want: win.VK_NUMPAD0},
		{name: "ordinary key", vk: 'A', want: 'A'},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeHPVK(test.vk, test.scan, test.extended); got != test.want {
				t.Fatalf("normalizeHPVK(%d,%d,%v)=%d want=%d", test.vk, test.scan, test.extended, got, test.want)
			}
		})
	}
}

func TestWalkKeyForVKUsesDistinctModifierKeys(t *testing.T) {
	tests := map[uint32]walk.Key{
		win.VK_LSHIFT: walk.KeyLShift, win.VK_RSHIFT: walk.KeyRShift,
		win.VK_LCONTROL: walk.KeyLControl, win.VK_RCONTROL: walk.KeyRControl,
		win.VK_LMENU: walk.KeyLAlt, win.VK_RMENU: walk.KeyRAlt,
		'A': walk.KeyA,
	}
	for vk, want := range tests {
		if got := walkKeyForVK(vk); got != want {
			t.Fatalf("walkKeyForVK(%d)=%d want=%d", vk, got, want)
		}
	}
}
