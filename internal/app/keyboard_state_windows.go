//go:build windows

package app

import (
	"time"

	"github.com/lxn/win"

	"firstlight/internal/kvm"
)

// resetKeyboardState forgets every locally tracked key. Callers send an
// all-keys-up report separately when a live connection exists.
func (w *appWindow) resetKeyboardState() {
	w.input.Lock()
	w.resetKeyboardStateLocked()
	w.input.Unlock()
}

// resetKeyboardStateLocked requires w.input to be held.
func (w *appWindow) resetKeyboardStateLocked() {
	w.pressed = make(map[Key]bool)
	w.rawPressed = [256]bool{}
	w.rawInput = false
	w.lastKeyReport = kvm.KeyboardReport(0)
	w.nextBackspace = time.Time{}
}

// resetCapturedInput extends the keyboard reset with pointer buttons.
// It is used whenever capture or the remote input session ends.
func (w *appWindow) resetCapturedInput() {
	w.input.Lock()
	w.resetKeyboardStateLocked()
	w.mouseButtons = 0
	w.input.Unlock()
}

// syncPressedModifiersLocked requires w.input to be held.
func (w *appWindow) syncPressedModifiersLocked() {
	setPressed(w.pressed, KeyLControl, keyDown(win.VK_LCONTROL))
	setPressed(w.pressed, KeyRControl, keyDown(win.VK_RCONTROL))
	setPressed(w.pressed, KeyControl, keyDown(win.VK_LCONTROL) || keyDown(win.VK_RCONTROL))

	setPressed(w.pressed, KeyLShift, keyDown(win.VK_LSHIFT))
	setPressed(w.pressed, KeyRShift, keyDown(win.VK_RSHIFT))
	setPressed(w.pressed, KeyShift, keyDown(win.VK_LSHIFT) || keyDown(win.VK_RSHIFT))

	setPressed(w.pressed, KeyLAlt, keyDown(win.VK_LMENU))
	setPressed(w.pressed, KeyRAlt, keyDown(win.VK_RMENU))
	setPressed(w.pressed, KeyAlt, keyDown(win.VK_LMENU))

	setPressed(w.pressed, KeyLWin, keyDown(win.VK_LWIN))
	setPressed(w.pressed, KeyRWin, keyDown(win.VK_RWIN))
}

func keyDown(vk int32) bool {
	return win.GetKeyState(vk) < 0
}

func setPressed(pressed map[Key]bool, key Key, down bool) {
	if down {
		pressed[key] = true
		return
	}
	delete(pressed, key)
}
