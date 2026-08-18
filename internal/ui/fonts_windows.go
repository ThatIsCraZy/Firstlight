//go:build windows

package ui

import (
	"os"
	"path/filepath"
	"sync"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
)

var (
	fontsOnce sync.Once
	fontFaces []font.FontFace
)

// systemFonts loads the Segoe UI family from the Windows font directory so
// the UI matches the platform's native typography without embedding fonts.
// It falls back to the Go fonts if the system files are unavailable.
func systemFonts() []font.FontFace {
	fontsOnce.Do(func() {
		dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
		if dir == "Fonts" {
			dir = `C:\Windows\Fonts`
		}
		for _, name := range []string{"segoeui.ttf", "seguisb.ttf", "segoeuib.ttf", "segoeuii.ttf"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			faces, err := opentype.ParseCollection(data)
			if err != nil {
				continue
			}
			fontFaces = append(fontFaces, faces...)
		}
		if len(fontFaces) == 0 {
			fontFaces = gofont.Collection()
		}
	})
	return fontFaces
}
