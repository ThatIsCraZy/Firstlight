//go:build windows

package app

import (
	"syscall"

	"github.com/lxn/win"
)

func (w *appWindow) installInputSink() {
	if w.MainWindow != nil {
		w.installWindowInputSink(w.MainWindow.Handle(), "main input sink", &w.mainWndProc, &w.oldMainWndProc)
	}
	if w.canvas != nil {
		w.installWindowInputSink(w.canvas.Handle(), "input sink", &w.canvasWndProc, &w.oldCanvasWndProc)
	}
}

func (w *appWindow) uninstallInputSink() {
	if w.canvas != nil {
		w.uninstallWindowInputSink(w.canvas.Handle(), "input sink", &w.canvasWndProc, &w.oldCanvasWndProc)
	}
	if w.MainWindow != nil {
		w.uninstallWindowInputSink(w.MainWindow.Handle(), "main input sink", &w.mainWndProc, &w.oldMainWndProc)
	}
}

func (w *appWindow) installWindowInputSink(hwnd win.HWND, label string, callback, previous *uintptr) {
	if hwnd == 0 || *callback != 0 {
		return
	}
	*callback = syscall.NewCallback(func(hwnd win.HWND, msg uint32, wp, lp uintptr) uintptr {
		if result, handled := w.handleInputMessage(msg, wp, lp); handled {
			return result
		}
		return win.CallWindowProc(*previous, hwnd, msg, wp, lp)
	})
	*previous = win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, *callback)
	w.logf("%s installed hwnd=0x%x old_proc=0x%x", label, uintptr(hwnd), *previous)
}

func (w *appWindow) uninstallWindowInputSink(hwnd win.HWND, label string, callback, previous *uintptr) {
	if hwnd == 0 || *callback == 0 || *previous == 0 {
		return
	}
	win.SetWindowLongPtr(hwnd, win.GWLP_WNDPROC, *previous)
	w.logf("%s uninstalled hwnd=0x%x", label, uintptr(hwnd))
	*callback = 0
	*previous = 0
}

func (w *appWindow) handleInputMessage(msg uint32, wp, lp uintptr) (uintptr, bool) {
	switch msg {
	case win.WM_GETDLGCODE:
		return win.DLGC_WANTALLKEYS | win.DLGC_WANTARROWS | win.DLGC_WANTCHARS | win.DLGC_WANTTAB, true
	case win.WM_KEYDOWN, win.WM_SYSKEYDOWN:
		return 0, w.handleRawKey(uint32(wp), uint32((lp>>16)&0xff), lp&(1<<24) != 0, true)
	case win.WM_KEYUP, win.WM_SYSKEYUP:
		return 0, w.handleRawKey(uint32(wp), uint32((lp>>16)&0xff), lp&(1<<24) != 0, false)
	case win.WM_CHAR, win.WM_SYSCHAR:
		return 0, true
	default:
		return 0, false
	}
}
