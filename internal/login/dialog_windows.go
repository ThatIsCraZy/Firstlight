//go:build windows

package login

import (
	"context"
	"image"
	"image/color"
	"strings"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"

	"firstlight/internal/ui"
	"firstlight/internal/uiicon"
)

type launcher struct {
	win   *app.Window
	th    *ui.Theme
	store credentialStore

	list     layout.List
	rows     []widget.Clickable
	selected int

	host, user, pass ui.TextField
	save             ui.Checkbox

	connectBtn, closeBtn, deleteBtn ui.Button
	modal                           ui.Modal

	connecting bool
	connect    func(Fields) error
}

// RunLauncher shows the multi-session launcher window and blocks until it is
// closed. It must be called from a non-main goroutine while app.Main runs on
// the main one.
func RunLauncher(ctx context.Context, initial Fields, connect func(Fields) error) error {
	_ = ctx

	store := loadCredentialStore()
	fillFromStore(&initial, store)

	l := &launcher{
		th:       ui.NewTheme(ui.SystemDark()),
		store:    store,
		selected: -1,
		connect:  connect,
	}
	l.list.Axis = layout.Vertical
	l.host.SetText(initial.Addr)
	l.user.SetText(initial.User)
	l.pass.SetText(initial.Password)

	w := new(app.Window)
	l.win = w
	w.Option(
		app.Title("Firstlight"),
		app.Size(unit.Dp(720), unit.Dp(400)),
		app.MinSize(unit.Dp(620), unit.Dp(360)),
	)

	var ops op.Ops
	var hwnd uintptr
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			return e.Err
		case app.Win32ViewEvent:
			hwnd = e.HWND
			uiicon.Apply(hwnd)
			ui.ApplyWindowChrome(hwnd, l.th.Dark, l.th.WindowBg)
		case app.FrameEvent:
			if dark := ui.SystemDark(); dark != l.th.Dark {
				l.th.SetDark(dark)
				ui.ApplyWindowChrome(hwnd, dark, l.th.WindowBg)
			}
			gtx := app.NewContext(&ops, e)
			l.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (l *launcher) entry(idx int) (credentialEntry, bool) {
	if idx < 0 || idx >= len(l.store.Entries) {
		return credentialEntry{}, false
	}
	return l.store.Entries[idx], true
}

func (l *launcher) fillEntry(e credentialEntry) bool {
	l.host.SetText(e.Addr)
	l.user.SetText(e.User)
	p, ok := e.password()
	if ok {
		l.pass.SetText(p)
	} else {
		l.pass.SetText("")
	}
	return ok
}

func (l *launcher) showError(title, message string) {
	l.modal.Show(title, message, ui.ModalButton{Label: "OK", Style: ui.ButtonPrimary})
}

func (l *launcher) connectFields(fields Fields) {
	if l.connect == nil {
		return
	}
	l.connecting = true
	if err := l.connect(fields); err != nil {
		l.showError("Verbindung fehlgeschlagen", err.Error())
	}
	l.connecting = false
}

func (l *launcher) submit() {
	fields := Fields{
		Addr:     strings.TrimSpace(l.host.Text()),
		User:     strings.TrimSpace(l.user.Text()),
		Password: l.pass.Text(),
	}
	if fields.Addr == "" || fields.User == "" || fields.Password == "" {
		l.showError("Firstlight login", "Adresse, Name und Passwort sind erforderlich.")
		return
	}
	if err := l.store.upsert(fields, l.save.Bool.Value); err != nil {
		l.showError("Firstlight login", err.Error())
		return
	}
	l.connectFields(fields)
}

func (l *launcher) activate(idx int) {
	e, ok := l.entry(idx)
	if !ok {
		return
	}
	hasPassword := l.fillEntry(e)
	if !hasPassword {
		return
	}
	fields := Fields{Addr: strings.TrimSpace(e.Addr), User: strings.TrimSpace(e.User), Password: l.pass.Text()}
	if err := l.store.upsert(fields, false); err != nil {
		l.showError("Firstlight login", err.Error())
		return
	}
	l.connectFields(fields)
}

func (l *launcher) deleteSelected() {
	e, ok := l.entry(l.selected)
	if !ok {
		return
	}
	if err := l.store.delete(e.Addr, e.User); err != nil {
		l.showError("Firstlight login", err.Error())
		return
	}
	l.selected = -1
}

func (l *launcher) layout(gtx layout.Context) {
	th := l.th
	ui.Fill(gtx, gtx.Constraints.Max, th.WindowBg)

	if l.connectBtn.Clicked(gtx) {
		l.submit()
	}
	if l.closeBtn.Clicked(gtx) {
		l.win.Perform(system.ActionClose)
	}
	if l.deleteBtn.Clicked(gtx) {
		l.deleteSelected()
	}
	if l.host.Submitted(gtx) || l.user.Submitted(gtx) || l.pass.Submitted(gtx) {
		l.submit()
	}

	layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(0.54, l.layoutListCard),
					layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
					layout.Flexed(0.46, l.layoutFormCard),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				_, hasSelection := l.entry(l.selected)
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return l.deleteBtn.Layout(gtx, th, ui.ButtonDestructive, hasSelection, "Delete")
					}),
					layout.Flexed(1, layout.Spacer{}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return l.closeBtn.Layout(gtx, th, ui.ButtonRegular, true, "Close")
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return l.connectBtn.Layout(gtx, th, ui.ButtonPrimary, !l.connecting, "Connect")
					}),
				)
			}),
		)
	})

	l.modal.Layout(gtx, th)
}

func (l *launcher) layoutListCard(gtx layout.Context) layout.Dimensions {
	th := l.th
	sz := gtx.Constraints.Max
	r := image.Rectangle{Max: sz}
	radius := gtx.Dp(10)
	ui.Shadow(gtx, r, radius, 2)
	ui.FillRRect(gtx, r, radius, th.CardBg)
	ui.StrokeRRect(gtx, r.Inset(1), radius-1, 1, th.Border)
	defer clip.UniformRRect(r, radius).Push(gtx.Ops).Pop()

	for len(l.rows) < len(l.store.Entries) {
		l.rows = append(l.rows, widget.Clickable{})
	}

	if len(l.store.Entries) == 0 {
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return th.Label(gtx, unit.Sp(13), font.Normal, th.TextSecondary, "Keine gespeicherten Verbindungen")
		})
		return layout.Dimensions{Size: sz}
	}

	layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return l.list.Layout(gtx, len(l.store.Entries), func(gtx layout.Context, idx int) layout.Dimensions {
			click := &l.rows[idx]
			for {
				c, ok := click.Update(gtx)
				if !ok {
					break
				}
				l.selected = idx
				if e, ok := l.entry(idx); ok {
					l.fillEntry(e)
				}
				if c.NumClicks >= 2 {
					l.activate(idx)
				}
			}
			return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return l.layoutRow(gtx, idx, click)
			})
		})
	})
	return layout.Dimensions{Size: sz}
}

func (l *launcher) layoutRow(gtx layout.Context, idx int, click *widget.Clickable) layout.Dimensions {
	th := l.th
	e := l.store.Entries[idx]
	height := gtx.Dp(40)
	width := gtx.Constraints.Max.X
	rowRect := image.Rect(0, 0, width, height)
	selected := idx == l.selected

	fg, sub := th.Text, th.TextSecondary
	if selected {
		ui.FillRRect(gtx, rowRect, gtx.Dp(7), th.Selection)
		fg, sub = th.OnAccent, ui.WithAlpha(th.OnAccent, 0xCC)
	} else if click.Hovered() {
		ui.FillRRect(gtx, rowRect, gtx.Dp(7), th.HoverFill)
	}

	gtx.Constraints.Min = image.Pt(width, height)
	gtx.Constraints.Max = image.Pt(width, height)
	layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return drawServerGlyph(gtx, th, fg)
		}),
		layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.Label(gtx, unit.Sp(13), font.Medium, fg, e.Addr)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return th.Label(gtx, unit.Sp(11), font.Normal, sub, e.User)
				}),
			)
		}),
	)
	return layout.Dimensions{Size: image.Pt(width, height)}
}

// drawServerGlyph draws a small rack-server pictogram.
func drawServerGlyph(gtx layout.Context, th *ui.Theme, col color.NRGBA) layout.Dimensions {
	size := gtx.Dp(20)
	unitH := (size - gtx.Dp(2)) / 2
	for i := 0; i < 2; i++ {
		y := i * (unitH + gtx.Dp(2))
		r := image.Rect(0, y, size, y+unitH)
		ui.StrokeRRect(gtx, r, gtx.Dp(2), 1, col)
		dot := image.Rect(gtx.Dp(3), y+unitH/2-1, gtx.Dp(6), y+unitH/2+1)
		ui.FillRRect(gtx, dot, 1, col)
	}
	return layout.Dimensions{Size: image.Pt(size, size)}
}

func (l *launcher) layoutFormCard(gtx layout.Context) layout.Dimensions {
	th := l.th
	sz := gtx.Constraints.Max
	r := image.Rectangle{Max: sz}
	radius := gtx.Dp(10)
	ui.Shadow(gtx, r, radius, 2)
	ui.FillRRect(gtx, r, radius, th.CardBg)
	ui.StrokeRRect(gtx, r.Inset(1), radius-1, 1, th.Border)

	layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return th.Label(gtx, unit.Sp(15), font.SemiBold, th.Text, "Verbindung")
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Rigid(l.formRow("Adresse", &l.host, false, "ilo-host[:port]")),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(l.formRow("Name", &l.user, false, "")),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			layout.Rigid(l.formRow("Passwort", &l.pass, true, "")),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(78)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return l.save.Layout(gtx, th, "Save password?")
				})
			}),
		)
	})
	return layout.Dimensions{Size: sz}
}

func (l *launcher) formRow(label string, field *ui.TextField, password bool, hint string) layout.Widget {
	th := l.th
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(70)
				gtx.Constraints.Max.X = gtx.Dp(70)
				return th.LabelAlign(gtx, unit.Sp(13), font.Normal, th.TextSecondary, label, text.End)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return field.Layout(gtx, th, password, hint)
			}),
		)
	}
}

func fillFromStore(fields *Fields, store credentialStore) {
	if fields.Addr == "" {
		return
	}
	if e, ok := store.find(fields.Addr); ok {
		if fields.User == "" {
			fields.User = e.User
		}
		if fields.Password == "" {
			if p, ok := e.password(); ok {
				fields.Password = p
			}
		}
	}
}
