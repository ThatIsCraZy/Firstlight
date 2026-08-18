package ui

import (
	"image"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/unit"
)

// StatusBar draws a slim bottom bar with a left and a right text segment.
func StatusBar(gtx layout.Context, th *Theme, left, right string) layout.Dimensions {
	height := gtx.Dp(26)
	width := gtx.Constraints.Max.X
	Fill(gtx, image.Pt(width, height), th.BarBg)
	func() {
		defer clip.Rect{Max: image.Pt(width, 1)}.Push(gtx.Ops).Pop()
		Fill(gtx, image.Pt(width, 1), th.Divider)
	}()
	gtx.Constraints.Min = image.Pt(width, height)
	gtx.Constraints.Max = image.Pt(width, height)
	layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
				return th.Label(gtx, unit.Sp(11.5), font.Normal, th.TextSecondary, left)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return th.Label(gtx, unit.Sp(11.5), font.Normal, th.TextSecondary, right)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
	)
	return layout.Dimensions{Size: image.Pt(width, height)}
}
