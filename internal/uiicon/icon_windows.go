//go:build windows

package uiicon

import (
	"bytes"
	_ "embed"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"sync"
	"unsafe"

	"github.com/lxn/win"
	xdraw "golang.org/x/image/draw"
)

//go:embed app_icon.png
var iconPNG []byte

type iconSet struct {
	small win.HICON // 16x16, title bar
	big   win.HICON // 32x32, Alt+Tab and taskbar
	err   error
}

var (
	iconOnce sync.Once
	icons    iconSet
)

// load decodes the embedded PNG once and builds the two icon sizes Windows
// asks for, resampled smoothly instead of letting the shell shrink 256px.
func load() iconSet {
	iconOnce.Do(func() {
		img, err := png.Decode(bytes.NewReader(iconPNG))
		if err != nil {
			icons.err = err
			return
		}
		source := image.NewRGBA(img.Bounds())
		draw.Draw(source, source.Bounds(), img, img.Bounds().Min, draw.Src)
		if icons.small, err = createIcon(source, 16); err != nil {
			icons.err = err
			return
		}
		if icons.big, err = createIcon(source, 32); err != nil {
			icons.err = err
			return
		}
	})
	return icons
}

func createIcon(source *image.RGBA, size int) (win.HICON, error) {
	scaled := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), source, source.Bounds(), xdraw.Src, nil)

	// CreateBitmap takes top-down scanlines in BGRA order.
	bgra := make([]byte, len(scaled.Pix))
	for i := 0; i < len(bgra); i += 4 {
		bgra[i+0] = scaled.Pix[i+2]
		bgra[i+1] = scaled.Pix[i+1]
		bgra[i+2] = scaled.Pix[i+0]
		bgra[i+3] = scaled.Pix[i+3]
	}
	color := win.CreateBitmap(int32(size), int32(size), 1, 32, unsafe.Pointer(&bgra[0]))
	if color == 0 {
		return 0, errors.New("create icon color bitmap failed")
	}
	defer win.DeleteObject(win.HGDIOBJ(color))
	// An all-zero mask keeps the color bitmap's own alpha channel.
	maskBits := make([]byte, ((size+15)/16*2)*size)
	mask := win.CreateBitmap(int32(size), int32(size), 1, 1, unsafe.Pointer(&maskBits[0]))
	if mask == 0 {
		return 0, errors.New("create icon mask bitmap failed")
	}
	defer win.DeleteObject(win.HGDIOBJ(mask))
	info := win.ICONINFO{FIcon: win.TRUE, HbmMask: mask, HbmColor: color}
	icon := win.CreateIconIndirect(&info)
	if icon == 0 {
		return 0, errors.New("create icon failed")
	}
	return icon, nil
}

// Apply sets the application icon on a native window. It is safe to call from
// any goroutine: the icon messages are posted, not sent.
func Apply(hwnd uintptr) {
	set := load()
	if set.err != nil || hwnd == 0 {
		return
	}
	const iconSmall, iconBig = 0, 1
	win.PostMessage(win.HWND(hwnd), win.WM_SETICON, iconSmall, uintptr(set.small))
	win.PostMessage(win.HWND(hwnd), win.WM_SETICON, iconBig, uintptr(set.big))
}
