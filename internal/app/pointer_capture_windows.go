//go:build windows

package app

import "github.com/lxn/win"

// pointerInsideCanvas reports whether the cursor is over the canvas area of
// the focused session window; x/y are window client coordinates.
func (w *appWindow) pointerInsideCanvas() (bool, int, int) {
	w.mu.Lock()
	hwnd := win.HWND(w.hwnd)
	rect := w.canvasRect
	blocked := w.uiBlocked
	w.mu.Unlock()
	if hwnd == 0 || blocked || rect.Dx() <= 0 || rect.Dy() <= 0 {
		return false, 0, 0
	}

	fg := win.GetForegroundWindow()
	if fg != hwnd && !win.IsChild(hwnd, fg) {
		return false, 0, 0
	}

	var pt win.POINT
	if !win.GetCursorPos(&pt) {
		return false, 0, 0
	}
	if !win.ScreenToClient(hwnd, &pt) {
		return false, 0, 0
	}
	x, y := int(pt.X), int(pt.Y)
	if x < rect.Min.X || y < rect.Min.Y || x >= rect.Max.X || y >= rect.Max.Y {
		return false, 0, 0
	}
	return true, x, y
}
