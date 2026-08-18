package ui

import (
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

type ButtonStyle int

const (
	ButtonRegular ButtonStyle = iota
	ButtonPrimary
	ButtonDestructive
)

// Button is a macOS-style push button.
type Button struct {
	Clickable widget.Clickable

	// enabled records the state the button was last drawn in, so a click on a
	// disabled button is never reported — not even later, once it is enabled
	// again.
	enabled bool
}

func (b *Button) Clicked(gtx layout.Context) bool {
	if !b.enabled {
		return false
	}
	return b.Clickable.Clicked(gtx)
}

func (b *Button) Layout(gtx layout.Context, th *Theme, style ButtonStyle, enabled bool, label string) layout.Dimensions {
	b.enabled = enabled
	if !enabled {
		// Discard pending presses: a disabled button must not fire them.
		for {
			if _, ok := b.Clickable.Update(gtx); !ok {
				break
			}
		}
		gtx = gtx.Disabled()
	}
	return b.Clickable.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		var bg, fg color.NRGBA
		border := false
		switch style {
		case ButtonPrimary:
			bg, fg = th.Accent, th.OnAccent
			if b.Clickable.Pressed() {
				bg = th.AccentPress
			} else if b.Clickable.Hovered() {
				bg = th.AccentHover
			}
		case ButtonDestructive:
			fg = th.Danger
			bg = buttonNeutralBg(th, &b.Clickable)
			border = !th.Dark
		default:
			fg = th.Text
			bg = buttonNeutralBg(th, &b.Clickable)
			border = !th.Dark
		}
		if !enabled {
			bg = WithAlpha(bg, bg.A/3)
			fg = th.TextDisabled
		}
		minWidth := gtx.Dp(72)
		height := gtx.Dp(28)
		macro := layout.Inset{Left: unit.Dp(14), Right: unit.Dp(14)}
		return layout.Background{}.Layout(gtx,
			func(gtx layout.Context) layout.Dimensions {
				sz := gtx.Constraints.Min
				r := image.Rectangle{Max: sz}
				radius := gtx.Dp(6)
				if style == ButtonPrimary && enabled {
					Shadow(gtx, r, radius, 1)
				}
				FillRRect(gtx, r, radius, bg)
				if border {
					StrokeRRect(gtx, r.Inset(1), radius-1, 1, th.Border)
				}
				pointer.CursorPointer.Add(gtx.Ops)
				return layout.Dimensions{Size: sz}
			},
			func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = height
				if gtx.Constraints.Min.X < minWidth {
					gtx.Constraints.Min.X = minWidth
				}
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return macro.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return widgetLabel(gtx, th.Shaper, font.Font{Weight: font.Medium}, unit.Sp(13), fg, label, text.Middle)
					})
				})
			},
		)
	})
}

func buttonNeutralBg(th *Theme, c *widget.Clickable) color.NRGBA {
	if th.Dark {
		bg := rgb(0x48484C)
		if c.Pressed() {
			bg = rgb(0x555559)
		} else if c.Hovered() {
			bg = rgb(0x505054)
		}
		return bg
	}
	bg := rgb(0xFFFFFF)
	if c.Pressed() {
		bg = rgb(0xE8E8ED)
	} else if c.Hovered() {
		bg = rgb(0xF4F4F7)
	}
	return bg
}

// TextField is a single-line macOS-style input with an optional focus ring.
type TextField struct {
	Editor widget.Editor
	init   bool
}

func (t *TextField) ensure(password bool) {
	if t.init {
		return
	}
	t.init = true
	t.Editor.SingleLine = true
	t.Editor.Submit = true
	if password {
		t.Editor.Mask = '•'
	}
}

func (t *TextField) Text() string { return t.Editor.Text() }

func (t *TextField) SetText(s string) { t.Editor.SetText(s) }

// Submitted reports whether Enter was pressed in the field this frame.
func (t *TextField) Submitted(gtx layout.Context) bool {
	submitted := false
	for {
		ev, ok := t.Editor.Update(gtx)
		if !ok {
			break
		}
		if _, isSubmit := ev.(widget.SubmitEvent); isSubmit {
			submitted = true
		}
	}
	return submitted
}

func (t *TextField) Layout(gtx layout.Context, th *Theme, password bool, hint string) layout.Dimensions {
	t.ensure(password)
	focused := gtx.Focused(&t.Editor)
	height := gtx.Dp(28)
	radius := gtx.Dp(6)
	padH := gtx.Dp(9)
	width := gtx.Constraints.Max.X
	if gtx.Constraints.Min.X > 0 && gtx.Constraints.Min.X < width {
		width = gtx.Constraints.Min.X
	}

	r := image.Rectangle{Max: image.Pt(width, height)}
	if focused {
		StrokeRRect(gtx, r.Inset(-2), radius+2, gtx.Dp(3), WithAlpha(th.Accent, 0x66))
	}
	FillRRect(gtx, r, radius, th.FieldBg)
	borderCol := th.Border
	if focused {
		borderCol = th.Accent
	}
	StrokeRRect(gtx, r.Inset(1), radius-1, 1, borderCol)
	pointer.CursorText.Add(gtx.Ops)

	// The editor must be laid out at the field's full inner width: given a zero
	// minimum an empty single-line editor reports zero width, which would leave
	// it with no clickable area and therefore unfocusable.
	inner := gtx
	innerW := width - 2*padH
	if innerW < 0 {
		innerW = 0
	}
	inner.Constraints = layout.Constraints{
		Min: image.Pt(innerW, 0),
		Max: image.Pt(innerW, height),
	}
	macro := op.Record(gtx.Ops)
	if t.Editor.Len() == 0 && hint != "" && !focused {
		hintGtx := inner
		hintMacro := op.Record(gtx.Ops)
		widgetLabel(hintGtx, th.Shaper, font.Font{}, unit.Sp(13), th.TextSecondary, hint, text.Start)
		hintMacro.Stop().Add(gtx.Ops)
	}
	dims := t.Editor.Layout(inner, th.Shaper, font.Font{}, unit.Sp(13), colorMaterial(gtx.Ops, th.Text), colorMaterial(gtx.Ops, WithAlpha(th.Accent, 0x66)))
	call := macro.Stop()
	offset := op.Offset(image.Pt(padH, (height-dims.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	offset.Pop()

	return layout.Dimensions{Size: image.Pt(width, height)}
}

// Checkbox is a macOS-style checkbox with a trailing label.
type Checkbox struct {
	Bool widget.Bool
}

func (c *Checkbox) Layout(gtx layout.Context, th *Theme, label string) layout.Dimensions {
	c.Bool.Update(gtx)
	return c.Bool.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				size := gtx.Dp(15)
				r := image.Rectangle{Max: image.Pt(size, size)}
				radius := gtx.Dp(4)
				if c.Bool.Value {
					FillRRect(gtx, r, radius, th.Accent)
					drawCheckmark(gtx, r, th.OnAccent)
				} else {
					FillRRect(gtx, r, radius, th.FieldBg)
					StrokeRRect(gtx, r.Inset(1), radius-1, 1, th.Border)
				}
				pointer.CursorPointer.Add(gtx.Ops)
				return layout.Dimensions{Size: r.Max}
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(7)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return widgetLabel(gtx, th.Shaper, font.Font{}, unit.Sp(13), th.Text, label, text.Start)
			}),
		)
	})
}

func drawCheckmark(gtx layout.Context, r image.Rectangle, col color.NRGBA) {
	w := float32(r.Dx())
	var p clip.Path
	p.Begin(gtx.Ops)
	p.MoveTo(f32Pt(r, 0.24, 0.52, w))
	p.LineTo(f32Pt(r, 0.43, 0.72, w))
	p.LineTo(f32Pt(r, 0.78, 0.30, w))
	defer clip.Stroke{Path: p.End(), Width: w * 0.14}.Op().Push(gtx.Ops).Pop()
	Fill(gtx, r.Max, col)
}

func f32Pt(r image.Rectangle, fx, fy float32, scale float32) f32.Point {
	return f32.Point{
		X: float32(r.Min.X) + fx*scale,
		Y: float32(r.Min.Y) + fy*scale,
	}
}
