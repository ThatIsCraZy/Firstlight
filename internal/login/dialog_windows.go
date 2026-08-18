package login

import (
	"context"
	"errors"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"

	"ilo-kvm/internal/uiicon"
)

func RunLauncher(ctx context.Context, initial Fields, connect func(Fields) error) error {
	_ = ctx

	store := loadCredentialStore()
	fillFromStore(&initial, store)

	var (
		mw        *walk.MainWindow
		history   *walk.TableView
		hostLE    *walk.LineEdit
		userLE    *walk.LineEdit
		passLE    *walk.LineEdit
		saveCB    *walk.CheckBox
		loginBtn  *walk.PushButton
		deleteBtn *walk.PushButton
		cancel    *walk.PushButton
		centered  bool
	)

	model := newConnectionListModel(store.Entries)

	selectedEntry := func() (credentialEntry, bool) {
		if history == nil {
			return credentialEntry{}, false
		}
		return model.entry(history.CurrentIndex())
	}

	updateDeleteButton := func() {
		if deleteBtn == nil {
			return
		}
		_, ok := selectedEntry()
		deleteBtn.SetEnabled(ok)
	}

	fillEntry := func(e credentialEntry) bool {
		if hostLE == nil || userLE == nil || passLE == nil {
			return false
		}
		_ = hostLE.SetText(e.Addr)
		_ = userLE.SetText(e.User)
		p, ok := e.password()
		if ok {
			_ = passLE.SetText(p)
		} else {
			_ = passLE.SetText("")
		}
		return ok
	}

	connectFields := func(fields Fields) {
		if connect == nil {
			return
		}
		loginBtn.SetEnabled(false)
		if err := connect(fields); err != nil {
			walk.MsgBox(mw, "iLO-KVM login", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		}
		loginBtn.SetEnabled(true)
	}

	submit := func() {
		fields := Fields{
			Addr:     strings.TrimSpace(hostLE.Text()),
			User:     strings.TrimSpace(userLE.Text()),
			Password: passLE.Text(),
		}
		if fields.Addr == "" || fields.User == "" || fields.Password == "" {
			walk.MsgBox(mw, "iLO-KVM login", "Adresse, Name und Passwort sind erforderlich.", walk.MsgBoxOK|walk.MsgBoxIconWarning)
			return
		}
		if err := store.upsert(fields, saveCB.Checked()); err != nil {
			walk.MsgBox(mw, "iLO-KVM login", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
			return
		}
		model.reset(store.Entries)
		connectFields(fields)
	}

	activateSelected := func() {
		e, ok := selectedEntry()
		if !ok {
			return
		}
		hasPassword := fillEntry(e)
		if !hasPassword {
			_ = passLE.SetFocus()
			return
		}
		fields := Fields{Addr: strings.TrimSpace(e.Addr), User: strings.TrimSpace(e.User), Password: passLE.Text()}
		if err := store.upsert(fields, false); err != nil {
			walk.MsgBox(mw, "iLO-KVM login", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
			return
		}
		model.reset(store.Entries)
		connectFields(fields)
	}

	deleteSelected := func() {
		e, ok := selectedEntry()
		if !ok {
			return
		}
		if err := store.delete(e.Addr, e.User); err != nil {
			walk.MsgBox(mw, "iLO-KVM login", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
			return
		}
		model.reset(store.Entries)
		if history != nil {
			_ = history.SetCurrentIndex(-1)
		}
		updateDeleteButton()
	}

	icon, iconErr := uiicon.Load()
	if iconErr == nil {
		defer icon.Dispose()
	}
	err := MainWindow{
		AssignTo: &mw,
		Title:    "iLO-KVM login",
		Icon:     icon,
		MinSize:  Size{Width: 720, Height: 260},
		Size:     Size{Width: 760, Height: 285},
		Layout:   VBox{MarginsZero: false, Spacing: 8},
		OnKeyDown: func(key walk.Key) {
			if key == walk.KeyReturn {
				submit()
			}
		},
		OnSizeChanged: func() {
			if centered || mw == nil {
				return
			}
			centered = true
			centerMainWindow(mw)
		},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 10},
				Children: []Widget{
					Composite{
						MinSize: Size{Width: 320, Height: 190},
						Layout:  VBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							TableView{
								AssignTo:                    &history,
								Model:                       model,
								HeaderHidden:                true,
								LastColumnStretched:         true,
								MultiSelection:              false,
								SelectionHiddenWithoutFocus: false,
								Columns: []TableViewColumn{
									{Title: "", Width: 28},
									{Title: "Address", Width: 170},
									{Title: "User", Width: 110},
								},
								StyleCell: func(style *walk.CellStyle) {
									if style.Col() == 0 && icon != nil {
										style.Image = icon
									}
								},
								OnCurrentIndexChanged: func() {
									if e, ok := selectedEntry(); ok {
										fillEntry(e)
									}
									updateDeleteButton()
								},
								OnItemActivated: activateSelected,
							},
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									HSpacer{},
									PushButton{AssignTo: &deleteBtn, Text: "Delete", Enabled: Bind("false"), OnClicked: deleteSelected},
								},
							},
						},
					},
					Composite{
						MinSize: Size{Width: 260, Height: 190},
						Layout:  VBox{MarginsZero: true, Spacing: 8},
						Children: []Widget{
							Composite{
								Layout: Grid{Columns: 2, Spacing: 8},
								Children: []Widget{
									Label{Text: "Adresse"},
									LineEdit{AssignTo: &hostLE, Text: initial.Addr},

									Label{Text: "Name"},
									LineEdit{AssignTo: &userLE, Text: initial.User},

									Label{Text: "Passwort"},
									LineEdit{AssignTo: &passLE, Text: initial.Password, PasswordMode: true},

									CheckBox{AssignTo: &saveCB, Text: "Save password?", MinSize: Size{Width: 190, Height: 24}, ColumnSpan: 2},
								},
							},
							VSpacer{},
						},
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &loginBtn, Text: "Connect", OnClicked: submit},
					PushButton{AssignTo: &cancel, Text: "Close", OnClicked: func() { _ = mw.Close() }},
				},
			},
		},
	}.Create()
	if err != nil {
		return err
	}
	code := mw.Run()
	if code != 0 {
		return errors.New("login window closed with error")
	}
	return nil
}

type connectionListModel struct {
	walk.TableModelBase
	entries []credentialEntry
}

func newConnectionListModel(entries []credentialEntry) *connectionListModel {
	m := &connectionListModel{}
	m.reset(entries)
	return m
}

func (m *connectionListModel) reset(entries []credentialEntry) {
	m.entries = append(m.entries[:0], entries...)
	m.PublishRowsReset()
}

func (m *connectionListModel) RowCount() int {
	return len(m.entries)
}

func (m *connectionListModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.entries) {
		return ""
	}
	switch col {
	case 1:
		return m.entries[row].Addr
	case 2:
		return m.entries[row].User
	default:
		return ""
	}
}

func (m *connectionListModel) entry(row int) (credentialEntry, bool) {
	if row < 0 || row >= len(m.entries) {
		return credentialEntry{}, false
	}
	return m.entries[row], true
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

func centerMainWindow(mw *walk.MainWindow) {
	b := mw.BoundsPixels()
	sw := int(win.GetSystemMetrics(win.SM_CXSCREEN))
	sh := int(win.GetSystemMetrics(win.SM_CYSCREEN))
	if sw <= 0 || sh <= 0 || b.Width <= 0 || b.Height <= 0 {
		return
	}
	x := (sw - b.Width) / 2
	y := (sh - b.Height) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	_ = mw.SetBoundsPixels(walk.Rectangle{X: x, Y: y, Width: b.Width, Height: b.Height})
}
