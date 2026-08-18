//go:build windows

package app

import (
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/win"
)

func chooseISOFile(initialPath string) (string, bool, error) {
	return runFileDialog("ISO mounten", initialPath, "ISO Images (*.iso)\x00*.iso\x00All Files (*.*)\x00*.*\x00", "iso", false)
}

func chooseKeyboardMapExport(initialPath string) (string, bool, error) {
	return runFileDialog("Export built-in German keyboard map", initialPath, "JSON keyboard maps (*.json)\x00*.json\x00All Files (*.*)\x00*.*\x00", "json", true)
}

func runFileDialog(title, initialPath, filter, defExt string, save bool) (string, bool, error) {
	var file [4096]uint16
	if initialPath != "" {
		initial, err := syscall.UTF16FromString(initialPath)
		if err == nil {
			copy(file[:len(file)-1], initial)
		}
	}
	titleU, _ := syscall.UTF16PtrFromString(title)
	// syscall.UTF16FromString rejects embedded NULs, which the filter needs.
	filterU := append(utf16.Encode([]rune(filter)), 0)
	defExtU, _ := syscall.UTF16PtrFromString(defExt)
	var initialDirU *uint16
	if initialPath != "" {
		if dir := filepath.Dir(initialPath); dir != "." {
			initialDirU, _ = syscall.UTF16PtrFromString(dir)
		}
	}
	ofn := win.OPENFILENAME{
		HwndOwner:       win.GetActiveWindow(),
		LpstrFilter:     &filterU[0],
		NFilterIndex:    1,
		LpstrFile:       &file[0],
		NMaxFile:        uint32(len(file)),
		LpstrInitialDir: initialDirU,
		LpstrTitle:      titleU,
		LpstrDefExt:     defExtU,
		Flags:           win.OFN_EXPLORER | win.OFN_ENABLESIZING | win.OFN_NOCHANGEDIR,
	}
	ofn.LStructSize = uint32(unsafe.Sizeof(ofn))
	var ok bool
	if save {
		ofn.Flags |= win.OFN_OVERWRITEPROMPT
		ok = win.GetSaveFileName(&ofn)
	} else {
		ofn.Flags |= win.OFN_FILEMUSTEXIST | win.OFN_PATHMUSTEXIST
		ok = win.GetOpenFileName(&ofn)
	}
	if !ok {
		return "", false, nil
	}
	selected := syscall.UTF16ToString(file[:])
	if strings.TrimSpace(selected) == "" {
		return "", false, nil
	}
	return selected, true, nil
}
