//go:build windows

package app

import (
	"testing"

	"github.com/lxn/win"
)

func TestHandleInputMessageStaticMessages(t *testing.T) {
	w := &appWindow{}
	result, handled := w.handleInputMessage(win.WM_GETDLGCODE, 0, 0)
	want := uintptr(win.DLGC_WANTALLKEYS | win.DLGC_WANTARROWS | win.DLGC_WANTCHARS | win.DLGC_WANTTAB)
	if !handled || result != want {
		t.Fatalf("WM_GETDLGCODE result=%#x handled=%v want=%#x", result, handled, want)
	}
	if result, handled = w.handleInputMessage(win.WM_CHAR, 'a', 0); !handled || result != 0 {
		t.Fatalf("WM_CHAR result=%#x handled=%v", result, handled)
	}
	if result, handled = w.handleInputMessage(win.WM_PAINT, 0, 0); handled || result != 0 {
		t.Fatalf("WM_PAINT result=%#x handled=%v", result, handled)
	}
}
