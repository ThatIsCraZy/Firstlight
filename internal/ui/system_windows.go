//go:build windows

package ui

import (
	"image/color"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

var (
	dwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	dwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	themeMu               sync.Mutex
	themeCached           bool
	themeCachedAt         time.Time
	themeDark             bool
)

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaCaptionColor         = 35
)

// SystemDark reports whether Windows apps are set to dark mode. The registry
// read is cached for one second so it can be polled every frame.
func SystemDark() bool {
	themeMu.Lock()
	defer themeMu.Unlock()
	if themeCached && time.Since(themeCachedAt) < time.Second {
		return themeDark
	}
	themeCached = true
	themeCachedAt = time.Now()
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return themeDark
	}
	defer key.Close()
	light, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return themeDark
	}
	themeDark = light == 0
	return themeDark
}

// ApplyWindowChrome switches the native title bar to match the theme:
// immersive dark mode plus a caption color equal to the window background
// (ignored gracefully on Windows versions without the attributes).
func ApplyWindowChrome(hwnd uintptr, dark bool, background color.NRGBA) {
	if hwnd == 0 {
		return
	}
	enable := int32(0)
	if dark {
		enable = 1
	}
	_, _, _ = dwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&enable)), unsafe.Sizeof(enable))
	colorref := uint32(background.R) | uint32(background.G)<<8 | uint32(background.B)<<16
	_, _, _ = dwmSetWindowAttribute.Call(hwnd, dwmwaCaptionColor, uintptr(unsafe.Pointer(&colorref)), unsafe.Sizeof(colorref))
}
