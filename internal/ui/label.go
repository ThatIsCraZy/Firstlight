package ui

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// colorMaterial records a ColorOp as a reusable CallOp, the form the
// low-level widget.Label/Editor APIs expect for their text color.
func colorMaterial(ops *op.Ops, c color.NRGBA) op.CallOp {
	m := op.Record(ops)
	paint.ColorOp{Color: c}.Add(ops)
	return m.Stop()
}

func widgetLabel(gtx layout.Context, shaper *text.Shaper, f font.Font, size unit.Sp, col color.NRGBA, txt string, align text.Alignment) layout.Dimensions {
	l := widget.Label{MaxLines: 1, Alignment: align}
	return l.Layout(gtx, shaper, f, size, txt, colorMaterial(gtx.Ops, col))
}

// MultilineLabel lays out wrapping text.
func (th *Theme) MultilineLabel(gtx layout.Context, size unit.Sp, weight font.Weight, col color.NRGBA, txt string) layout.Dimensions {
	l := widget.Label{}
	return l.Layout(gtx, th.Shaper, font.Font{Weight: weight}, size, txt, colorMaterial(gtx.Ops, col))
}
