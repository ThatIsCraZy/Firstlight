//go:build windows

package app

import (
	"syscall"

	"gioui.org/io/system"
	"github.com/lxn/win"
)

func closeAction() system.Action { return system.ActionClose }

func focusWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	win.ShowWindow(win.HWND(hwnd), win.SW_SHOW)
	win.SetForegroundWindow(win.HWND(hwnd))
}

// installInputSink subclasses the Gio window procedure so raw keyboard and
// mouse messages reach the KVM path before Gio processes them.
func (w *appWindow) installInputSink(hwnd uintptr) {
	if hwnd == 0 || w.oldWndProc != 0 {
		return
	}
	if w.wndProc == 0 {
		w.wndProc = syscall.NewCallback(func(hwnd win.HWND, msg uint32, wp, lp uintptr) uintptr {
			if result, handled := w.handleInputMessage(msg, wp, lp); handled {
				return result
			}
			prev := w.oldWndProc
			if prev == 0 {
				return win.DefWindowProc(hwnd, msg, wp, lp)
			}
			return win.CallWindowProc(prev, hwnd, msg, wp, lp)
		})
	}
	w.oldWndProc = win.SetWindowLongPtr(win.HWND(hwnd), win.GWLP_WNDPROC, w.wndProc)
	w.logf("input sink installed hwnd=0x%x old_proc=0x%x", hwnd, w.oldWndProc)
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
	case win.WM_ACTIVATE:
		if wp&0xffff == 0 { // WA_INACTIVE
			w.releaseCapture()
		}
		return 0, false
	default:
		return 0, false
	}
}
