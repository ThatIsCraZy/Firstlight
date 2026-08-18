package ui

import (
	"image"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
)

// MenuItem is one entry of a dropdown menu.
type MenuItem struct {
	Text      string
	Checked   bool
	Enabled   bool
	Separator bool
	Do        func()
}

// MenuDef describes one menu of the bar; items are rebuilt every frame so
// enabled/checked state is always current.
type MenuDef struct {
	Title string
	Items []MenuItem
}

// MenuBar is an in-window, macOS-style menu bar with dropdowns.
type MenuBar struct {
	open        int // index of the open menu, -1 when closed
	titleClicks []widget.Clickable
	itemClicks  []widget.Clickable
	scrim       widget.Clickable
	titleXs     []int
	barHeight   int
}

func NewMenuBar() *MenuBar { return &MenuBar{open: -1} }

func (m *MenuBar) ensure(menus []MenuDef) {
	for len(m.titleClicks) < len(menus) {
		m.titleClicks = append(m.titleClicks, widget.Clickable{})
	}
	total := 0
	for _, menu := range menus {
		total += len(menu.Items)
	}
	for len(m.itemClicks) < total {
		m.itemClicks = append(m.itemClicks, widget.Clickable{})
	}
	for len(m.titleXs) < len(menus) {
		m.titleXs = append(m.titleXs, 0)
	}
}

// Layout draws the bar; an open dropdown is drawn deferred so it overlays the
// rest of the frame.
func (m *MenuBar) Layout(gtx layout.Context, th *Theme, menus []MenuDef) layout.Dimensions {
	m.ensure(menus)
	barHeight := gtx.Dp(32)
	m.barHeight = barHeight
	width := gtx.Constraints.Max.X

	Fill(gtx, image.Pt(width, barHeight), th.BarBg)
	Fill(gtx, image.Pt(width, barHeight), th.BarBg) // opaque over any canvas bleed
	// bottom divider
	func() {
		defer clip.Rect{Min: image.Pt(0, barHeight-1), Max: image.Pt(width, barHeight)}.Push(gtx.Ops).Pop()
		Fill(gtx, image.Pt(width, barHeight), th.Divider)
	}()

	x := gtx.Dp(8)
	for i := range menus {
		m.titleXs[i] = x
		click := &m.titleClicks[i]
		if click.Clicked(gtx) {
			if m.open == i {
				m.open = -1
			} else {
				m.open = i
			}
		}
		if m.open >= 0 && m.open != i && click.Hovered() {
			m.open = i
		}
		trans := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
		dims := click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			padH := gtx.Dp(10)
			lblMacro := op.Record(gtx.Ops)
			fg := th.Text
			if m.open == i {
				fg = th.OnAccent
			}
			lblGtx := gtx
			lblGtx.Constraints.Min = image.Point{}
			lblDims := widgetLabel(lblGtx, th.Shaper, font.Font{Weight: font.Medium}, unit.Sp(13), fg, menus[i].Title, text.Start)
			lblCall := lblMacro.Stop()
			w := lblDims.Size.X + 2*padH
			r := image.Rect(0, gtx.Dp(4), w, barHeight-gtx.Dp(4))
			if m.open == i {
				FillRRect(gtx, r, gtx.Dp(5), th.Accent)
			} else if click.Hovered() {
				FillRRect(gtx, r, gtx.Dp(5), th.HoverFill)
			}
			pointer.CursorPointer.Add(gtx.Ops)
			offset := op.Offset(image.Pt(padH, (barHeight-lblDims.Size.Y)/2)).Push(gtx.Ops)
			lblCall.Add(gtx.Ops)
			offset.Pop()
			return layout.Dimensions{Size: image.Pt(w, barHeight)}
		})
		trans.Pop()
		x += dims.Size.X
	}

	if m.open >= 0 && m.open < len(menus) {
		m.layoutDropdown(gtx, th, menus)
	}
	return layout.Dimensions{Size: image.Pt(width, barHeight)}
}

func (m *MenuBar) layoutDropdown(gtx layout.Context, th *Theme, menus []MenuDef) {
	// Deferred overlay: scrim over the whole window plus the popup card.
	macro := op.Record(gtx.Ops)

	winSize := gtx.Constraints.Max
	// Scrim catches clicks outside the popup and closes the menu.
	if m.scrim.Clicked(gtx) {
		m.open = -1
	}
	m.scrim.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.Rect{Max: winSize}.Push(gtx.Ops).Pop()
		return layout.Dimensions{Size: winSize}
	})

	if m.open >= 0 {
		itemBase := 0
		for i := 0; i < m.open; i++ {
			itemBase += len(menus[i].Items)
		}
		items := menus[m.open].Items

		itemH := gtx.Dp(26)
		sepH := gtx.Dp(9)
		padV := gtx.Dp(5)
		checkW := gtx.Dp(20)
		padR := gtx.Dp(16)

		// measure widest label
		maxW := gtx.Dp(160)
		for _, it := range items {
			mm := op.Record(gtx.Ops)
			mGtx := gtx
			mGtx.Constraints.Min = image.Point{}
			d := widgetLabel(mGtx, th.Shaper, font.Font{}, unit.Sp(13), th.Text, it.Text, text.Start)
			_ = mm.Stop()
			if w := d.Size.X + checkW + padR; w > maxW {
				maxW = w
			}
		}
		height := 2 * padV
		for _, it := range items {
			if it.Separator {
				height += sepH
			} else {
				height += itemH
			}
		}

		pos := image.Pt(m.titleXs[m.open], m.barHeight+gtx.Dp(2))
		card := image.Rectangle{Min: pos, Max: pos.Add(image.Pt(maxW, height))}
		radius := gtx.Dp(8)
		Shadow(gtx, card, radius, 4)
		FillRRect(gtx, card, radius, th.MenuBg)
		StrokeRRect(gtx, card.Inset(1), radius-1, 1, th.Border)

		y := card.Min.Y + padV
		for idx, it := range items {
			if it.Separator {
				line := image.Rect(card.Min.X+gtx.Dp(10), y+sepH/2, card.Max.X-gtx.Dp(10), y+sepH/2+1)
				func() {
					defer clip.Rect(line).Push(gtx.Ops).Pop()
					Fill(gtx, winSize, th.Divider)
				}()
				y += sepH
				continue
			}
			click := &m.itemClicks[itemBase+idx]
			if it.Enabled && click.Clicked(gtx) {
				m.open = -1
				if it.Do != nil {
					it.Do()
				}
			}
			rowRect := image.Rect(card.Min.X+gtx.Dp(5), y, card.Max.X-gtx.Dp(5), y+itemH)
			trans := op.Offset(rowRect.Min).Push(gtx.Ops)
			rowGtx := gtx
			rowGtx.Constraints = layout.Exact(rowRect.Size())
			if !it.Enabled {
				rowGtx = rowGtx.Disabled()
			}
			item := it
			click.Layout(rowGtx, func(gtx layout.Context) layout.Dimensions {
				sz := gtx.Constraints.Min
				fg := th.Text
				if !item.Enabled {
					fg = th.TextDisabled
				} else if click.Hovered() {
					FillRRect(gtx, image.Rectangle{Max: sz}, gtx.Dp(5), th.Accent)
					fg = th.OnAccent
				}
				if item.Checked {
					cr := image.Rect(gtx.Dp(6), (sz.Y-gtx.Dp(11))/2, gtx.Dp(6)+gtx.Dp(11), (sz.Y+gtx.Dp(11))/2)
					drawCheckmark(gtx, cr, fg)
				}
				lblMacro := op.Record(gtx.Ops)
				lblGtx := gtx
				lblGtx.Constraints.Min = image.Point{}
				d := widgetLabel(lblGtx, th.Shaper, font.Font{}, unit.Sp(13), fg, item.Text, text.Start)
				call := lblMacro.Stop()
				off := op.Offset(image.Pt(checkW, (sz.Y-d.Size.Y)/2)).Push(gtx.Ops)
				call.Add(gtx.Ops)
				off.Pop()
				if item.Enabled {
					pointer.CursorPointer.Add(gtx.Ops)
				}
				return layout.Dimensions{Size: sz}
			})
			trans.Pop()
			y += itemH
		}
	}

	op.Defer(gtx.Ops, macro.Stop())
}

// Open reports whether a dropdown is currently open.
func (m *MenuBar) Open() bool { return m.open >= 0 }

// Close closes any open dropdown.
func (m *MenuBar) Close() { m.open = -1 }
