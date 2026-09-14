package console

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"time"

	"firstlight/internal/bmc"
	"firstlight/internal/idrac"
)

// This file holds the Dell half of a console session. The iLO path in
// session.go is untouched: a Session either carries a remote iDRAC session or
// it does not, and every public method asks that question first.

// openIDRACSession connects to a Dell controller and wraps it in the same
// Session type the iLO path produces.
func openIDRACSession(rootCtx, connectCtx context.Context, opts OpenOptions, isoRoot *ISORoot, logf func(string, ...any)) (*Session, error) {
	remote, err := idrac.Connect(connectCtx, idrac.Config{
		Addr:       opts.Address,
		User:       opts.Username,
		Password:   opts.Password,
		VerifyCert: !opts.InsecureSkipVerify,
		Logf:       logf,
	})
	if err != nil {
		return nil, err
	}
	sessionCtx, cancel := context.WithCancel(rootCtx)
	remoteState := remote.State()
	s := &Session{
		ctx:               sessionCtx,
		cancel:            cancel,
		logf:              logf,
		address:           opts.Address,
		connected:         true,
		inputReady:        remoteState.InputReady,
		protocolVersion:   rfbProtocolVersion,
		width:             remoteState.Width,
		height:            remoteState.Height,
		revision:          1,
		power:             "unknown",
		openedAt:          time.Now().UTC(),
		insecureTransport: opts.InsecureSkipVerify,
		notify:            make(chan struct{}),
		isoRoot:           isoRoot,
		operations:        make(map[string]*operationRecord),
		done:              make(chan struct{}),
		remote:            remote,
	}
	go s.watchRemote()
	return s, nil
}

// rfbProtocolVersion is reported in State.ProtocolVersion for Dell sessions.
// The iLO path uses 1 and 2 for its two wire protocols, so 3 marks RFB without
// colliding with either.
const rfbProtocolVersion = 3

// watchRemote mirrors the Dell session's state into the fields the rest of the
// package reads, and keeps the change notification working.
func (s *Session) watchRemote() {
	for {
		changed := s.remote.Changed()
		state := s.remote.State()
		s.mu.Lock()
		s.inputReady = state.InputReady
		s.shared = state.Shared
		s.width = state.Width
		s.height = state.Height
		s.frameRevision = state.Revision
		if state.Revision > 0 {
			s.lastFrameAt = time.Now().UTC()
		}
		switch {
		case !state.PowerKnown:
			s.power = "unknown"
		case state.PowerOn:
			s.power = "on"
		default:
			s.power = "off"
		}
		s.waitingApproval = state.WaitingForApproval
		if !state.Connected && s.connected {
			s.connected = false
			s.inputReady = false
			s.disconnectReason = state.Disconnect
			if s.disconnectReason == "" {
				s.disconnectReason = "console connection closed"
			}
		}
		s.signalChangeLocked()
		stop := !s.connected
		s.mu.Unlock()
		if stop {
			return
		}
		select {
		case <-changed:
		case <-s.remote.Done():
			s.markRemoteDisconnected()
			return
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Session) markRemoteDisconnected() {
	state := s.remote.State()
	reason := state.Disconnect
	if reason == "" {
		reason = "console connection closed"
	}
	s.mu.Lock()
	if s.connected {
		s.connected = false
		s.inputReady = false
		s.disconnectReason = reason
		s.signalChangeLocked()
	}
	s.mu.Unlock()
}

// isRemote reports whether this session talks to a Dell controller.
func (s *Session) isRemote() bool { return s.remote != nil }

func (s *Session) remoteSnapshotPNG(available bool) ([]byte, error) {
	if !available {
		return nil, nil
	}
	frame := s.remote.Frame(nil)
	var buf bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buf, frame); err != nil {
		return nil, fmt.Errorf("encode console frame: %w", err)
	}
	return buf.Bytes(), nil
}

func (s *Session) remoteTypeText(ctx context.Context, text string, delay time.Duration) (TextResult, error) {
	sent, skipped, err := s.remote.TypeText(ctx, text, delay)
	return TextResult{Sent: sent, Skipped: skipped}, err
}

func (s *Session) remotePressKeys(ctx context.Context, names []string, hold time.Duration) error {
	modifier, keys, err := parseChord(names)
	if err != nil {
		return err
	}
	report := hidReport(modifier, keys...)
	if err := s.remote.SendKeyboardReport(report); err != nil {
		return err
	}
	if err := waitContext(ctx, hold); err != nil {
		_ = s.remote.SendAllKeysUp()
		return err
	}
	return s.remote.SendAllKeysUp()
}

// hidReport builds the same ten-byte report kvm.KeyboardReport produces. The
// Dell backend translates it into key events, so both vendors share the chord
// parser and the keyboard layout files.
func hidReport(modifier byte, keys ...byte) [10]byte {
	var report [10]byte
	report[0] = 1
	report[2] = modifier
	for i, key := range keys {
		if i >= 6 {
			break
		}
		report[4+i] = key
	}
	return report
}

func (s *Session) remoteMouse(ctx context.Context, action string, x, y int, button string, wheel int8) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		action = "move"
	}
	s.mu.Lock()
	width, height := s.width, s.height
	buttons := s.mouseButtons
	s.mu.Unlock()
	if width <= 0 || height <= 0 {
		return errors.New("console dimensions are unavailable")
	}
	if x < 0 || y < 0 || x >= width || y >= height {
		return fmt.Errorf("mouse coordinates (%d,%d) are outside %dx%d console", x, y, width, height)
	}
	mask, err := mouseButtonMask(button)
	if err != nil {
		return err
	}
	send := func(current byte, currentWheel int8) error {
		if err := s.remote.SendMouse(x, y, 0, 0, width, height, currentWheel, current); err != nil {
			return err
		}
		s.mu.Lock()
		s.mouseX, s.mouseY, s.mouseButtons = x, y, current
		s.mu.Unlock()
		return nil
	}
	switch action {
	case "move":
		if button != "" {
			return errors.New("button is not valid for mouse move")
		}
		return send(buttons, 0)
	case "scroll":
		if button != "" {
			return errors.New("button is not valid for mouse scroll")
		}
		if wheel == 0 {
			return errors.New("wheel must be non-zero for mouse scroll")
		}
		return send(buttons, wheel)
	case "button_down":
		if mask == 0 {
			return errors.New("button is required for button_down")
		}
		return send(buttons|mask, 0)
	case "button_up":
		if mask == 0 {
			return errors.New("button is required for button_up")
		}
		return send(buttons&^mask, 0)
	case "click":
		if mask == 0 {
			return errors.New("button is required for click")
		}
		if err := send(buttons|mask, 0); err != nil {
			return err
		}
		if err := waitContext(ctx, 80*time.Millisecond); err != nil {
			_ = send(buttons, 0)
			return err
		}
		return send(buttons, 0)
	default:
		return fmt.Errorf("unknown mouse action %q", action)
	}
}

func (s *Session) remotePower(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return s.remote.Power(ctx, idrac.PowerAction(action))
}

func (s *Session) remoteManagementStatus(ctx context.Context) (ManagementStatus, error) {
	status, err := s.remote.ManagementStatus(ctx)
	if err != nil {
		return ManagementStatus{}, err
	}
	return ManagementStatus{
		PowerState: status.PowerState,
		BootOverride: BootOverrideStatus{
			Target:  status.BootOverride.Target,
			Enabled: status.BootOverride.Enabled,
			Mode:    status.BootOverride.Mode,
		},
	}, nil
}

func (s *Session) remoteSetOneTimeBoot(ctx context.Context, device string) (OneTimeBootResult, error) {
	change, err := s.remote.SetOneTimeBoot(ctx, device)
	result := OneTimeBootResult{
		Device: change.Device,
		Before: BootOverrideStatus{
			Target:  change.Before.Target,
			Enabled: change.Before.Enabled,
			Mode:    change.Before.Mode,
		},
		Current: BootOverrideStatus{
			Target:  change.Current.Target,
			Enabled: change.Current.Enabled,
			Mode:    change.Current.Mode,
		},
		Verified: change.Verified,
	}
	return result, err
}

// detectVendor asks the address which controller it is. A failure is not
// fatal: the caller falls back to the iLO path, which then reports a precise
// error of its own.
func detectVendor(ctx context.Context, opts OpenOptions) bmc.Vendor {
	details, err := bmc.Detect(ctx, bmc.Options{
		Addr:       opts.Address,
		VerifyCert: !opts.InsecureSkipVerify,
	})
	if err != nil {
		return bmc.VendorUnknown
	}
	return details.Vendor
}
