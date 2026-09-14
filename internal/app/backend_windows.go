//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"firstlight/internal/bmc"
	"firstlight/internal/idrac"
	"firstlight/internal/kvm"
	"firstlight/internal/ui"
	"firstlight/internal/vmedia"
)

// consoleBackend is everything the window needs from a live console,
// regardless of which controller is on the other end. The iLO connection
// satisfies it as it stands; the Dell session reaches it through the adapter
// below, so the keyboard pipeline, the layout files and the clipboard typist
// stay shared between both vendors.
type consoleBackend interface {
	SendAllKeysUp() error
	SendKeyboardReport(report [10]byte) error
	SendCtrlAltDel() error
	SendMouse(absX, absY, relX, relY, width, height int, wheel int8, buttons byte) error
	SendPower(option kvm.PowerOption) error
	SendRefresh() error
}

// A compile-time check that the iLO connection needs no adapter at all.
var _ consoleBackend = (*kvm.Conn)(nil)

// idracBackend adapts a Dell console session to consoleBackend.
type idracBackend struct {
	session *idrac.Session
}

func (b idracBackend) SendAllKeysUp() error { return b.session.SendAllKeysUp() }

func (b idracBackend) SendKeyboardReport(report [10]byte) error {
	return b.session.SendKeyboardReport(report)
}

// SendCtrlAltDel goes out as an ordinary report. The iLO path needs a special
// sequence here; the Dell firmware translates the three keysyms itself.
func (b idracBackend) SendCtrlAltDel() error {
	var report [10]byte
	report[0] = 1
	report[2] = 0x01 | 0x04 // left control and left alt
	report[4] = 76          // delete
	if err := b.session.SendKeyboardReport(report); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	return b.session.SendAllKeysUp()
}

func (b idracBackend) SendMouse(absX, absY, relX, relY, width, height int, wheel int8, buttons byte) error {
	return b.session.SendMouse(absX, absY, relX, relY, width, height, wheel, buttons)
}

func (b idracBackend) SendPower(option kvm.PowerOption) error {
	action, err := powerActionFor(option)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return b.session.Power(ctx, action)
}

func (b idracBackend) SendRefresh() error { return b.session.Refresh() }

func powerActionFor(option kvm.PowerOption) (idrac.PowerAction, error) {
	switch option {
	case kvm.PowerMomentaryPress:
		return idrac.PowerMomentaryPress, nil
	case kvm.PowerPressAndHold:
		return idrac.PowerPressAndHold, nil
	case kvm.PowerColdBoot:
		return idrac.PowerColdBootAction, nil
	case kvm.PowerReset:
		return idrac.PowerResetAction, nil
	}
	return "", fmt.Errorf("unknown power option %d", option)
}

// detectVendor asks the address which controller answers. A failure falls back
// to the iLO path, which then reports a precise error of its own.
func (w *appWindow) detectVendor(cfg Config) bmc.Vendor {
	ctx, cancel := context.WithTimeout(w.ctx, 8*time.Second)
	defer cancel()
	details, err := bmc.Detect(ctx, bmc.Options{Addr: cfg.Addr, VerifyCert: cfg.VerifyCert})
	if err != nil {
		w.logf("vendor detection failed, assuming iLO: %v", err)
		return bmc.VendorUnknown
	}
	w.logf("detected %s product=%q id=%q", details.Vendor, details.Product, details.Identifier)
	return details.Vendor
}

// connectIDRAC opens a Dell console and hands it to the window.
func (w *appWindow) connectIDRAC(cfg Config) error {
	session, err := idrac.Connect(w.ctx, idrac.Config{
		Addr:       cfg.Addr,
		User:       cfg.User,
		Password:   cfg.Password,
		VerifyCert: cfg.VerifyCert,
		// Share keeps other viewers connected; without it the firmware is
		// asked to displace them.
		// -seize means the operator wants the console even if someone else has
		// it, so it both clears the other sessions and asks the firmware for an
		// exclusive one.
		Exclusive:          cfg.Seize,
		SeizeOtherSessions: cfg.Seize,
		Logf:               w.logf,
		TraceDecoder:       cfg.Debug,
	})
	if err != nil {
		return err
	}
	state := session.State()
	w.mu.Lock()
	w.remote = session
	w.sender = idracBackend{session: session}
	w.frameReady = false
	w.sharedSession = state.Shared
	w.inputReady = state.InputReady
	w.captured = false
	w.vmISOPath = ""
	w.serverPower = "unknown"
	w.postCode = ""
	w.resetCapturedInput()
	w.mu.Unlock()

	if cfg.ISOPath != "" {
		if err := w.mountRemoteISO(cfg.ISOPath); err != nil {
			_ = session.Close()
			w.mu.Lock()
			w.remote, w.sender = nil, nil
			w.mu.Unlock()
			return err
		}
	}
	go w.watchRemote(session)
	w.logf("iDRAC console connected %dx%d shared=%v", state.Width, state.Height, state.Shared)
	return nil
}

// watchRemote mirrors the Dell session into the window state and repaints when
// a frame arrives.
func (w *appWindow) watchRemote(session *idrac.Session) {
	first := true
	sharedReported := false
	for {
		changed := session.Changed()
		state := session.State()

		w.mu.Lock()
		current := w.remote == session
		if current {
			w.inputReady = state.InputReady
			w.sharedSession = state.Shared
			w.serverPower = w.serverPowerText(state)
			if state.Revision > 0 {
				w.frameReady = true
			}
		}
		w.mu.Unlock()
		if !current {
			return
		}
		// A console that is already in use turns this session into a shared
		// one. The firmware then holds the video back until the current viewer
		// allows access, or denies it when nobody answers. Without a word in
		// the status bar that looks like a hung connection.
		// A console that stays dark after the handshake is held by another
		// session. Saying so beats leaving a black window on screen.
		if state.WaitingForApproval && !sharedReported {
			sharedReported = true
			w.logf("console held by another session, %d other viewer(s) listed", len(state.OtherUsers))
			w.setStatus("Console in use by another session: waiting for the current viewer to allow sharing.")
		}
		if state.Revision > 0 {
			if first {
				first = false
				w.logf("first frame %dx%d, power=%v shared=%v",
					state.Width, state.Height, w.serverPowerText(state), state.Shared)
				_ = session.SendAllKeysUp()
			}
			w.markFrameDirty()
		}
		w.invalidate()

		if !state.Connected {
			reason := state.Disconnect
			if reason == "" {
				reason = "console connection closed"
			}
			w.handleDisconnect("Disconnected: " + reason)
			return
		}
		select {
		case <-changed:
		case <-session.Done():
			w.handleDisconnect("Disconnected: console connection closed")
			return
		case <-w.ctx.Done():
			return
		}
	}
}

// serverPowerText renders the power state for the log and the status bar.
func (w *appWindow) serverPowerText(state idrac.State) string {
	switch {
	case !state.PowerKnown:
		return "unknown"
	case state.PowerOn:
		return "on"
	default:
		return "off"
	}
}

// mountRemoteISO attaches an image over the Dell media channel.
func (w *appWindow) mountRemoteISO(path string) error {
	w.mu.Lock()
	session := w.remote
	w.mu.Unlock()
	if session == nil {
		return errors.New("console is not connected")
	}
	iso, err := vmedia.OpenISO(path)
	if err != nil {
		return fmt.Errorf("ISO file cannot be opened: %w", err)
	}
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()
	if err := session.MountISO(ctx, iso, filepath.Base(path)); err != nil {
		_ = iso.Close()
		return err
	}
	w.mu.Lock()
	w.vmISOPath = path
	w.mu.Unlock()
	return nil
}

// unmountRemoteISO detaches the image again.
func (w *appWindow) unmountRemoteISO() error {
	w.mu.Lock()
	session := w.remote
	w.vmISOPath = ""
	w.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.UnmountISO()
}

// isRemote reports whether the window drives a Dell controller.
func (w *appWindow) isRemote() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.remote != nil
}

// mediaMounted reports whether an image is attached, whichever backend is live.
func (w *appWindow) mediaMounted() bool {
	w.mu.Lock()
	iloMounted := w.vm != nil
	session := w.remote
	w.mu.Unlock()
	if session == nil {
		return iloMounted
	}
	mounted, _, _, _ := session.MediaStatus()
	return mounted
}

// mountISORemote attaches an image on the Dell path, off the frame goroutine.
func (w *appWindow) mountISORemote(path string) {
	w.mu.Lock()
	if w.vmConnecting {
		w.mu.Unlock()
		w.setStatus("ISO mount is still in progress.")
		return
	}
	w.vmConnecting = true
	w.status = fmt.Sprintf("Mounting ISO: %s", filepath.Base(path))
	w.mu.Unlock()
	w.invalidate()

	go func() {
		err := w.mountRemoteISO(path)
		w.mu.Lock()
		w.vmConnecting = false
		if w.closed {
			w.mu.Unlock()
			return
		}
		if err != nil {
			w.vmISOPath = ""
			w.status = fmt.Sprintf("ISO mount failed: %v", err)
			w.logf("iso mount failed path=%q: %v", path, err)
		} else {
			w.status = fmt.Sprintf("ISO mounted: %s", filepath.Base(path))
			w.logf("iso mounted path=%q", path)
		}
		w.mu.Unlock()
		w.invalidate()
	}()
}

// dismountISORemote detaches the image on the Dell path.
func (w *appWindow) dismountISORemote() {
	w.mu.Lock()
	if w.vmConnecting {
		w.mu.Unlock()
		w.setStatus("ISO mount is still in progress.")
		return
	}
	path := w.vmISOPath
	w.mu.Unlock()
	if !w.mediaMounted() {
		w.setStatus("No ISO is mounted.")
		return
	}
	if err := w.unmountRemoteISO(); err != nil {
		w.setStatus(fmt.Sprintf("ISO dismount failed: %v", err))
		return
	}
	w.logf("iso dismounted path=%q", path)
	w.setStatus("ISO dismounted.")
}

// takeOverConsole ends the other sessions of this user on the controller, so
// the console slots they hold come free. It displaces whoever is on the
// console, which is why the menu asks first.
func (w *appWindow) takeOverConsole() {
	w.mu.Lock()
	session := w.remote
	w.mu.Unlock()
	if session == nil {
		w.setStatus("Taking over the console is only available on Dell controllers.")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
		defer cancel()
		closed, err := session.TakeOverConsole(ctx)
		if err != nil {
			w.setStatus(fmt.Sprintf("Take-over failed: %v", err))
			w.logf("console take-over failed: %v", err)
			return
		}
		if closed == 0 {
			w.setStatus("No other session was holding the console.")
			return
		}
		w.setStatus(fmt.Sprintf("Closed %d other session(s). Reconnect to take the console.", closed))
		w.logf("console take-over closed %d session(s)", closed)
	}()
}

// confirmTakeOverConsole asks before displacing another viewer.
func (w *appWindow) confirmTakeOverConsole() {
	w.ui(func() {
		w.modal.Show("Firstlight Console",
			"Close the other sessions of this user on the controller? "+
				"Anyone else working on this console loses their connection.",
			ui.ModalButton{Label: "Cancel", Style: ui.ButtonRegular, Action: func() {
				w.logf("console take-over cancelled")
			}},
			ui.ModalButton{Label: "Take Over", Style: ui.ButtonDestructive, Action: w.takeOverConsole},
		)
	})
}

// listOtherSessions writes the other sessions to the status bar and the log,
// so the operator can see who is holding the console before displacing them.
func (w *appWindow) listOtherSessions() {
	w.mu.Lock()
	session := w.remote
	w.mu.Unlock()
	if session == nil {
		w.setStatus("Session list is only available on Dell controllers.")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(w.ctx, 20*time.Second)
		defer cancel()
		sessions, err := session.OtherSessions(ctx)
		if err != nil {
			w.setStatus(fmt.Sprintf("Session list failed: %v", err))
			return
		}
		if len(sessions) == 0 {
			w.setStatus("No other session is open on this controller.")
			return
		}
		for _, item := range sessions {
			w.logf("session %s: user=%q from=%s type=%s", item.ID, item.UserName, item.ClientIP, item.Type)
		}
		w.setStatus(fmt.Sprintf("%d other session(s) open, see the log for details.", len(sessions)))
	}()
}
