//go:build windows

package app

import "github.com/lxn/win"

func (w *appWindow) pointerInsideCanvas() (bool, int, int) {
	if w.MainWindow == nil || w.canvas == nil {
		return false, 0, 0
	}
	main := w.MainWindow.Handle()
	canvas := w.canvas.Handle()
	if main == 0 || canvas == 0 {
		return false, 0, 0
	}

	fg := win.GetForegroundWindow()
	if fg != main && !win.IsChild(main, fg) {
		return false, 0, 0
	}

	var pt win.POINT
	if !win.GetCursorPos(&pt) {
		return false, 0, 0
	}
	var rect win.RECT
	if !win.GetWindowRect(canvas, &rect) {
		return false, 0, 0
	}
	if pt.X < rect.Left || pt.Y < rect.Top || pt.X >= rect.Right || pt.Y >= rect.Bottom {
		return false, 0, 0
	}
	return true, int(pt.X - rect.Left), int(pt.Y - rect.Top)
}
