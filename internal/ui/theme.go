// Package ui implements Firstlight's custom Gio widget set: a lean,
// macOS-inspired look with light and dark palettes that follow the
// Windows system theme.
package ui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
)

type Palette struct {
	WindowBg      color.NRGBA // window background
	CardBg        color.NRGBA // raised surfaces (lists, form cards)
	FieldBg       color.NRGBA // text field background
	MenuBg        color.NRGBA // dropdown menus, modal cards
	BarBg         color.NRGBA // menu bar / status bar background
	Border        color.NRGBA // hairline borders
	Divider       color.NRGBA // separators
	Text          color.NRGBA
	TextSecondary color.NRGBA
	TextDisabled  color.NRGBA
	Accent        color.NRGBA // macOS system blue
	AccentHover   color.NRGBA
	AccentPress   color.NRGBA
	OnAccent      color.NRGBA
	Danger        color.NRGBA
	Selection     color.NRGBA // list selection background
	HoverFill     color.NRGBA // subtle hover wash
	CanvasBg      color.NRGBA // KVM video surround
}

func LightPalette() Palette {
	return Palette{
		WindowBg:      rgb(0xF2F2F7),
		CardBg:        rgb(0xFFFFFF),
		FieldBg:       rgb(0xFFFFFF),
		MenuBg:        rgb(0xFCFCFD),
		BarBg:         rgb(0xF7F7FA),
		Border:        rgba(0x00, 0x00, 0x00, 0x24),
		Divider:       rgba(0x00, 0x00, 0x00, 0x14),
		Text:          rgb(0x1D1D1F),
		TextSecondary: rgb(0x6E6E73),
		TextDisabled:  rgba(0x3C, 0x3C, 0x43, 0x4D),
		Accent:        rgb(0x007AFF),
		AccentHover:   rgb(0x0071EB),
		AccentPress:   rgb(0x0064D2),
		OnAccent:      rgb(0xFFFFFF),
		Danger:        rgb(0xFF3B30),
		Selection:     rgb(0x007AFF),
		HoverFill:     rgba(0x00, 0x00, 0x00, 0x0A),
		CanvasBg:      rgb(0x121212),
	}
}

func DarkPalette() Palette {
	return Palette{
		WindowBg:      rgb(0x1E1E20),
		CardBg:        rgb(0x2E2E31),
		FieldBg:       rgb(0x1C1C1E),
		MenuBg:        rgb(0x323236),
		BarBg:         rgb(0x28282B),
		Border:        rgba(0xFF, 0xFF, 0xFF, 0x2A),
		Divider:       rgba(0xFF, 0xFF, 0xFF, 0x16),
		Text:          rgb(0xF5F5F7),
		TextSecondary: rgb(0x98989D),
		TextDisabled:  rgba(0xEB, 0xEB, 0xF5, 0x4D),
		Accent:        rgb(0x0A84FF),
		AccentHover:   rgb(0x2492FF),
		AccentPress:   rgb(0x3E9FFF),
		OnAccent:      rgb(0xFFFFFF),
		Danger:        rgb(0xFF453A),
		Selection:     rgb(0x0A84FF),
		HoverFill:     rgba(0xFF, 0xFF, 0xFF, 0x0F),
		CanvasBg:      rgb(0x121212),
	}
}

type Theme struct {
	Shaper *text.Shaper
	Dark   bool
	Palette
}

// NewTheme builds a theme with the system font stack and the palette
// matching dark.
func NewTheme(dark bool) *Theme {
	th := &Theme{Shaper: text.NewShaper(text.WithCollection(systemFonts()))}
	th.SetDark(dark)
	return th
}

func (th *Theme) SetDark(dark bool) {
	th.Dark = dark
	if dark {
		th.Palette = DarkPalette()
	} else {
		th.Palette = LightPalette()
	}
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

func rgba(r, g, b, a uint8) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: a}
}

// WithAlpha returns c with its alpha replaced.
func WithAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// Fill paints the rectangle sz in the current clip with c.
func Fill(gtx layout.Context, sz image.Point, c color.NRGBA) {
	defer clip.Rect{Max: sz}.Push(gtx.Ops).Pop()
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// FillRRect paints a rounded rectangle.
func FillRRect(gtx layout.Context, r image.Rectangle, radius int, c color.NRGBA) {
	defer clip.UniformRRect(r, radius).Push(gtx.Ops).Pop()
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// StrokeRRect draws a hairline rounded-rect border.
func StrokeRRect(gtx layout.Context, r image.Rectangle, radius, width int, c color.NRGBA) {
	rr := clip.UniformRRect(r, radius)
	defer clip.Stroke{Path: rr.Path(gtx.Ops), Width: float32(width)}.Op().Push(gtx.Ops).Pop()
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// Shadow approximates a soft drop shadow under a rounded rect by layering
// progressively larger, more transparent rounded rects.
func Shadow(gtx layout.Context, r image.Rectangle, radius int, elevation int) {
	if elevation <= 0 {
		return
	}
	base := uint8(36)
	for i := elevation; i > 0; i-- {
		grow := i
		alpha := uint8(int(base) / (i + 1))
		sr := image.Rect(r.Min.X-grow, r.Min.Y-grow+1, r.Max.X+grow, r.Max.Y+grow+2)
		FillRRect(gtx, sr, radius+grow, color.NRGBA{A: alpha})
	}
}

// Label lays out a single-line text label.
func (th *Theme) Label(gtx layout.Context, size unit.Sp, weight font.Weight, col color.NRGBA, txt string) layout.Dimensions {
	return widgetLabel(gtx, th.Shaper, font.Font{Weight: weight}, size, col, txt, text.Start)
}

func (th *Theme) LabelAlign(gtx layout.Context, size unit.Sp, weight font.Weight, col color.NRGBA, txt string, align text.Alignment) layout.Dimensions {
	return widgetLabel(gtx, th.Shaper, font.Font{Weight: weight}, size, col, txt, align)
}
