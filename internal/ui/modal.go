package ui

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
	"gioui.org/widget"
)

// ModalButton is one action of a modal dialog.
type ModalButton struct {
	Label  string
	Style  ButtonStyle
	Action func()
}

// Modal is a macOS-style alert sheet rendered as a deferred overlay.
// Only one dialog is shown at a time; Show replaces any visible one.
type Modal struct {
	visible bool
	title   string
	message string
	buttons []ModalButton
	btns    []Button
	scrim   widget.Clickable
}

func (m *Modal) Show(title, message string, buttons ...ModalButton) {
	m.visible = true
	m.title = title
	m.message = message
	m.buttons = buttons
	for len(m.btns) < len(buttons) {
		m.btns = append(m.btns, Button{})
	}
}

func (m *Modal) Hide()         { m.visible = false }
func (m *Modal) Visible() bool { return m.visible }

// Layout draws the modal if visible. Call after the main content so the
// overlay is deferred on top.
func (m *Modal) Layout(gtx layout.Context, th *Theme) {
	if !m.visible {
		return
	}
	macro := op.Record(gtx.Ops)
	winSize := gtx.Constraints.Max

	// Dim the window; swallow clicks (the scrim does not dismiss: dialogs
	// here are explicit-choice confirmations).
	m.scrim.Clicked(gtx)
	m.scrim.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: winSize}.Push(gtx.Ops).Pop()
		Fill(gtx, winSize, color.NRGBA{A: 0x55})
		return layout.Dimensions{Size: winSize}
	})

	// Measure the card content.
	cardW := gtx.Dp(380)
	if cardW > winSize.X-gtx.Dp(32) {
		cardW = winSize.X - gtx.Dp(32)
	}
	contentMacro := op.Record(gtx.Ops)
	contentGtx := gtx
	contentGtx.Constraints = layout.Constraints{
		Min: image.Pt(cardW, 0),
		Max: image.Pt(cardW, winSize.Y),
	}
	contentDims := m.layoutCard(contentGtx, th)
	contentCall := contentMacro.Stop()

	pos := image.Pt((winSize.X-contentDims.Size.X)/2, (winSize.Y-contentDims.Size.Y)/2)
	if pos.Y < gtx.Dp(40) {
		pos.Y = gtx.Dp(40)
	}
	card := image.Rectangle{Min: pos, Max: pos.Add(contentDims.Size)}
	radius := gtx.Dp(11)
	Shadow(gtx, card, radius, 6)
	FillRRect(gtx, card, radius, th.MenuBg)
	StrokeRRect(gtx, card.Inset(1), radius-1, 1, th.Border)
	trans := op.Offset(pos).Push(gtx.Ops)
	contentCall.Add(gtx.Ops)
	trans.Pop()

	op.Defer(gtx.Ops, macro.Stop())
}

func (m *Modal) layoutCard(gtx layout.Context, th *Theme) layout.Dimensions {
	return layout.UniformInset(unit.Dp(18)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.Label(gtx, unit.Sp(14), font.SemiBold, th.Text, m.title)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.MultilineLabel(gtx, unit.Sp(13), font.Normal, th.TextSecondary, m.message)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(18)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, 2*len(m.buttons)+1)
				children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
				for i := range m.buttons {
					i := i
					if i > 0 {
						children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout))
					}
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						btn := &m.btns[i]
						def := m.buttons[i]
						if btn.Clicked(gtx) {
							m.visible = false
							if def.Action != nil {
								def.Action()
							}
						}
						return btn.Layout(gtx, th, def.Style, true, def.Label)
					}))
				}
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
			}),
		)
	})
}
