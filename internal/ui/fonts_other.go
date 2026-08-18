//go:build !windows

package ui

import (
	"gioui.org/font"
	"gioui.org/font/gofont"
)

func systemFonts() []font.FontFace {
	return gofont.Collection()
}
