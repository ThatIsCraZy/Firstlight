package ui

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// harness drives a widget through Gio's event router without a GPU window, so
// pointer interaction can be asserted deterministically.
type harness struct {
	router input.Router
	ops    op.Ops
	size   image.Point
}

func newHarness(w, h int) *harness {
	return &harness{size: image.Pt(w, h)}
}

// frame lays out fn once and submits the resulting ops to the router.
func (t *harness) frame(fn func(gtx layout.Context)) {
	t.ops.Reset()
	gtx := layout.Context{
		Ops:         &t.ops,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(t.size),
		Source:      t.router.Source(),
	}
	fn(gtx)
	t.router.Frame(&t.ops)
}

// click sends a press/release pair at p, each followed by a frame so the
// widget can observe the events.
func (t *harness) click(fn func(gtx layout.Context), p image.Point) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	t.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos})
	t.frame(fn)
	t.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Position: pos, Buttons: pointer.ButtonPrimary})
	t.frame(fn)
	t.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos})
	t.frame(fn)
}

func testMenus(triggered *string) []MenuDef {
	return []MenuDef{
		{Title: "Edit", Items: []MenuItem{
			{Text: "Paste Clipboard", Enabled: true, Do: func() { *triggered = "paste" }},
		}},
		{Title: "Power", Items: []MenuItem{
			{Text: "Momentary Press", Enabled: true, Do: func() { *triggered = "momentary" }},
			{Text: "Reset", Enabled: false, Do: func() { *triggered = "reset" }},
		}},
	}
}

func TestMenuBarOpensDropdownOnTitleClick(t *testing.T) {
	th := NewTheme(true)
	bar := NewMenuBar()
	var triggered string
	h := newHarness(800, 600)
	layoutBar := func(gtx layout.Context) { bar.Layout(gtx, th, testMenus(&triggered)) }

	// Establish the title positions before clicking one.
	h.frame(layoutBar)
	if bar.Open() {
		t.Fatal("menu bar starts open")
	}

	h.click(layoutBar, image.Pt(30, 16)) // "Edit"
	if !bar.Open() {
		t.Fatal("clicking a menu title did not open its dropdown")
	}

	h.click(layoutBar, image.Pt(30, 16))
	if bar.Open() {
		t.Fatal("clicking the open title again did not close the dropdown")
	}
}

func TestMenuBarInvokesEnabledItemOnly(t *testing.T) {
	th := NewTheme(true)
	bar := NewMenuBar()
	var triggered string
	h := newHarness(800, 600)
	layoutBar := func(gtx layout.Context) { bar.Layout(gtx, th, testMenus(&triggered)) }

	h.frame(layoutBar)
	h.click(layoutBar, image.Pt(30, 16)) // open "Edit"
	if !bar.Open() {
		t.Fatal("dropdown did not open")
	}

	// The first item sits just below the bar; its row starts after the
	// dropdown's top padding.
	h.click(layoutBar, image.Pt(60, 32+2+5+13))
	if triggered != "paste" {
		t.Fatalf("clicking the first item triggered %q, want %q", triggered, "paste")
	}
	if bar.Open() {
		t.Fatal("selecting an item left the dropdown open")
	}

	triggered = ""
	h.frame(layoutBar)
	h.click(layoutBar, image.Pt(70, 16)) // open "Power"
	if !bar.Open() {
		t.Fatal("second menu did not open")
	}
	// Second row is the disabled "Reset" entry.
	h.click(layoutBar, image.Pt(100, 32+2+5+26+13))
	if triggered != "" {
		t.Fatalf("disabled item triggered %q", triggered)
	}
}
