//go:build windows

package app

import (
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"firstlight/internal/kvm"
)

// resetKeyboardStateLocked forgets every locally tracked key. Callers must hold
// w.mu and send an all-keys-up report separately when a live connection exists.
func (w *appWindow) resetKeyboardStateLocked() {
	w.pressed = make(map[walk.Key]bool)
	w.rawPressed = [256]bool{}
	w.rawInput = false
	w.lastKeyReport = kvm.KeyboardReport(0)
	w.nextBackspace = time.Time{}
}

// resetCapturedInputLocked extends the keyboard reset with pointer buttons.
// It is used whenever capture or the remote input session ends.
func (w *appWindow) resetCapturedInputLocked() {
	w.resetKeyboardStateLocked()
	w.mouseButtons = 0
}

func (w *appWindow) syncPressedModifiersLocked() {
	setPressed(w.pressed, walk.KeyLControl, keyDown(win.VK_LCONTROL))
	setPressed(w.pressed, walk.KeyRControl, keyDown(win.VK_RCONTROL))
	setPressed(w.pressed, walk.KeyControl, keyDown(win.VK_LCONTROL) || keyDown(win.VK_RCONTROL))

	setPressed(w.pressed, walk.KeyLShift, keyDown(win.VK_LSHIFT))
	setPressed(w.pressed, walk.KeyRShift, keyDown(win.VK_RSHIFT))
	setPressed(w.pressed, walk.KeyShift, keyDown(win.VK_LSHIFT) || keyDown(win.VK_RSHIFT))

	setPressed(w.pressed, walk.KeyLAlt, keyDown(win.VK_LMENU))
	setPressed(w.pressed, walk.KeyRAlt, keyDown(win.VK_RMENU))
	setPressed(w.pressed, walk.KeyAlt, keyDown(win.VK_LMENU))

	setPressed(w.pressed, walk.KeyLWin, keyDown(win.VK_LWIN))
	setPressed(w.pressed, walk.KeyRWin, keyDown(win.VK_RWIN))
}

func keyDown(vk int32) bool {
	return win.GetKeyState(vk) < 0
}

func setPressed(pressed map[walk.Key]bool, key walk.Key, down bool) {
	if down {
		pressed[key] = true
		return
	}
	delete(pressed, key)
}
