//go:build windows

package app

import (
	"errors"
	"syscall"
	"unsafe"

	"github.com/lxn/win"
)

func readClipboardText() (string, error) {
	if !win.IsClipboardFormatAvailable(win.CF_UNICODETEXT) {
		return "", errors.New("clipboard does not contain text")
	}
	if !win.OpenClipboard(0) {
		return "", errors.New("open clipboard failed")
	}
	defer win.CloseClipboard()

	handle := win.GetClipboardData(win.CF_UNICODETEXT)
	if handle == 0 {
		return "", errors.New("get clipboard text failed")
	}
	ptr := win.GlobalLock(win.HGLOBAL(handle))
	if ptr == nil {
		return "", errors.New("lock clipboard text failed")
	}
	defer win.GlobalUnlock(win.HGLOBAL(handle))

	p := (*uint16)(ptr)
	n := 0
	for {
		if *(*uint16)(unsafe.Add(unsafe.Pointer(p), n*2)) == 0 {
			break
		}
		n++
	}
	buf := unsafe.Slice(p, n)
	return syscall.UTF16ToString(buf), nil
}
