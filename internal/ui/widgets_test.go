package ui

import (
	"image"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"
)

func TestTextFieldAcceptsTypedTextAfterClick(t *testing.T) {
	th := NewTheme(true)
	var field TextField
	h := newHarness(400, 100)
	layoutField := func(gtx layout.Context) {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		field.Submitted(gtx)
		field.Layout(gtx, th, false, "hint")
	}

	h.frame(layoutField)
	h.click(layoutField, image.Pt(60, 14))

	h.router.Queue(key.EditEvent{Text: "10.0.0.1"})
	h.frame(layoutField)

	if got := field.Text(); got != "10.0.0.1" {
		t.Fatalf("field text = %q, want %q", got, "10.0.0.1")
	}
}

func TestTextFieldReportsSubmitOnEnter(t *testing.T) {
	th := NewTheme(false)
	var field TextField
	h := newHarness(400, 100)
	submitted := false
	layoutField := func(gtx layout.Context) {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if field.Submitted(gtx) {
			submitted = true
		}
		field.Layout(gtx, th, true, "")
	}

	h.frame(layoutField)
	h.click(layoutField, image.Pt(60, 14))
	h.router.Queue(key.EditEvent{Text: "secret"})
	h.frame(layoutField)
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame(layoutField)
	h.frame(layoutField)

	if !submitted {
		t.Fatal("pressing Enter in the field did not report a submit")
	}
}

func TestButtonReportsClick(t *testing.T) {
	th := NewTheme(true)
	var btn Button
	h := newHarness(400, 100)
	clicks := 0
	lay := func(gtx layout.Context) {
		if btn.Clicked(gtx) {
			clicks++
		}
		btn.Layout(gtx, th, ButtonPrimary, true, "Connect")
	}
	h.frame(lay)
	h.click(lay, image.Pt(40, 14))
	h.frame(lay)
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1", clicks)
	}
}

func TestButtonIgnoresClickWhenDisabled(t *testing.T) {
	th := NewTheme(true)
	var btn Button
	h := newHarness(400, 100)
	clicks := 0
	lay := func(gtx layout.Context) {
		if btn.Clicked(gtx) {
			clicks++
		}
		btn.Layout(gtx, th, ButtonRegular, false, "Delete")
	}
	h.frame(lay)
	h.click(lay, image.Pt(40, 14))
	h.frame(lay)
	if clicks != 0 {
		t.Fatalf("disabled button reported %d clicks", clicks)
	}
}

func TestCheckboxTogglesOnClick(t *testing.T) {
	th := NewTheme(false)
	var box Checkbox
	h := newHarness(400, 100)
	lay := func(gtx layout.Context) { box.Layout(gtx, th, "Save password?") }
	h.frame(lay)
	h.click(lay, image.Pt(7, 7))
	h.frame(lay)
	if !box.Bool.Value {
		t.Fatal("checkbox did not toggle on")
	}
	h.click(lay, image.Pt(7, 7))
	h.frame(lay)
	if box.Bool.Value {
		t.Fatal("checkbox did not toggle back off")
	}
}

func TestModalButtonRunsActionAndHides(t *testing.T) {
	th := NewTheme(true)
	var modal Modal
	h := newHarness(600, 400)
	confirmed := false
	modal.Show("Firstlight Power", "Send power command?",
		ModalButton{Label: "Cancel", Style: ButtonRegular},
		ModalButton{Label: "Reset", Style: ButtonDestructive, Action: func() { confirmed = true }},
	)
	lay := func(gtx layout.Context) { modal.Layout(gtx, th) }
	h.frame(lay)
	if !modal.Visible() {
		t.Fatal("modal is not visible after Show")
	}
	// Card geometry at PxPerDp=1: width 380 centered in 600 gives x 110..490,
	// inset 18 puts the rightmost button's right edge at 472 and the action row
	// at roughly y 216..244.
	h.click(lay, image.Pt(440, 230))
	h.frame(lay)
	if !confirmed {
		t.Fatal("modal action did not run")
	}
	if modal.Visible() {
		t.Fatal("modal stayed visible after an action")
	}
}
