package idrac

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"firstlight/internal/rfb"
)

// RFB orders the pointer button bits left, middle, right, while Firstlight's
// input layer follows the USB HID order left, right, middle. The two wheel
// bits have no counterpart in a HID report and are added per scroll event.
const (
	rfbButtonLeft   = 1 << 0
	rfbButtonMiddle = 1 << 1
	rfbButtonRight  = 1 << 2
	rfbWheelUp      = 1 << 3
	rfbWheelDown    = 1 << 4

	hidButtonLeft   = 1 << 0
	hidButtonRight  = 1 << 1
	hidButtonMiddle = 1 << 2
)

// SendKeyboardReport accepts a HID keyboard report, the same shape the iLO
// path uses, and turns it into the key events RFB expects.
func (s *Session) SendKeyboardReport(report [10]byte) error {
	if err := s.ready(); err != nil {
		return err
	}
	s.mu.Lock()
	events := s.keyboard.Apply(report)
	s.mu.Unlock()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, event := range events {
		if err := s.rfb.KeyEvent(event.Down, event.Keysym); err != nil {
			return err
		}
	}
	return nil
}

// SendAllKeysUp releases everything that is currently held.
func (s *Session) SendAllKeysUp() error {
	return s.SendKeyboardReport([10]byte{1})
}

// SendKeysym presses and releases one keysym directly. Clipboard typing uses
// this because a character maps to a keysym without a layout detour.
func (s *Session) SendKeysym(keysym uint32, hold time.Duration) error {
	if err := s.ready(); err != nil {
		return err
	}
	s.writeMu.Lock()
	err := s.rfb.KeyEvent(true, keysym)
	s.writeMu.Unlock()
	if err != nil {
		return err
	}
	if hold > 0 {
		time.Sleep(hold)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.rfb.KeyEvent(false, keysym)
}

// TypeText sends text as key events. The BMC maps each keysym to a scan code
// itself, so no local keyboard layout is involved.
func (s *Session) TypeText(ctx context.Context, text string, delay time.Duration) (sent, skipped int, err error) {
	if err := s.ready(); err != nil {
		return 0, 0, err
	}
	if delay <= 0 {
		delay = 8 * time.Millisecond
	}
	for _, char := range text {
		if err := ctx.Err(); err != nil {
			return sent, skipped, err
		}
		if char == '\r' {
			continue
		}
		keysym := rfb.KeysymForRune(char)
		if keysym == 0 {
			skipped++
			continue
		}
		if err := s.SendKeysym(keysym, delay); err != nil {
			return sent, skipped, err
		}
		if err := sleepContext(ctx, delay); err != nil {
			return sent, skipped, err
		}
		sent++
	}
	return sent, skipped, nil
}

// SendMouse reports an absolute pointer position. The signature matches the
// iLO path so the user interface can call either backend the same way. The
// relative deltas stay unused, but the width and height do not: callers report
// the position in the space they draw in, which is the scaled window rectangle
// in the graphical client and the framebuffer itself everywhere else. RFB
// carries framebuffer coordinates, so a position from a differently sized
// viewport is rescaled before it goes on the wire.
func (s *Session) SendMouse(absX, absY, _, _, width, height int, wheel int8, hidButtons byte) error {
	if err := s.ready(); err != nil {
		return err
	}
	absX, absY = s.toFramebuffer(absX, absY, width, height)
	mask := rfbButtons(hidButtons)
	s.mu.Lock()
	s.buttons = mask
	s.mu.Unlock()

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.rfb.PointerEvent(mask, absX, absY); err != nil {
		return err
	}
	if wheel == 0 {
		return nil
	}
	// A wheel notch is a press and release of button four or five.
	notch := byte(rfbWheelUp)
	if wheel < 0 {
		notch = rfbWheelDown
	}
	steps := int(wheel)
	if steps < 0 {
		steps = -steps
	}
	for i := 0; i < steps && i < 8; i++ {
		if err := s.rfb.PointerEvent(mask|notch, absX, absY); err != nil {
			return err
		}
		if err := s.rfb.PointerEvent(mask, absX, absY); err != nil {
			return err
		}
	}
	return nil
}

// toFramebuffer maps a pointer sample taken in a viewport of the given size
// onto framebuffer coordinates.
func (s *Session) toFramebuffer(x, y, width, height int) (int, int) {
	s.mu.Lock()
	frameWidth, frameHeight := s.state.Width, s.state.Height
	s.mu.Unlock()
	return scaleAxis(x, width, frameWidth), scaleAxis(y, height, frameHeight)
}

// scaleAxis maps one axis from a viewport of length from onto a framebuffer of
// length to. The first and the last column of the viewport land on the first
// and the last column of the framebuffer, which keeps the screen edges
// reachable: a pointer pushed against the left edge of a scaled view has to
// arrive at column zero, or the remote Start button stays out of reach. A
// viewport of the same length, or one of unknown length, passes the value
// through untouched.
func scaleAxis(v, from, to int) int {
	if to <= 0 {
		if v < 0 {
			return 0
		}
		return v
	}
	if from > 1 && from != to {
		v = int(math.Round(float64(v) * float64(to-1) / float64(from-1)))
	}
	if v < 0 {
		return 0
	}
	if v > to-1 {
		return to - 1
	}
	return v
}

func rfbButtons(hid byte) byte {
	var mask byte
	if hid&hidButtonLeft != 0 {
		mask |= rfbButtonLeft
	}
	if hid&hidButtonMiddle != 0 {
		mask |= rfbButtonMiddle
	}
	if hid&hidButtonRight != 0 {
		mask |= rfbButtonRight
	}
	return mask
}

// PowerAction names a power operation in the vocabulary Firstlight already
// uses for iLO.
type PowerAction string

const (
	PowerMomentaryPress PowerAction = "momentary_press"
	PowerPressAndHold   PowerAction = "press_and_hold"
	PowerColdBootAction PowerAction = "cold_boot"
	PowerResetAction    PowerAction = "reset"
)

// Power performs a power operation. The console channel carries the same
// commands the browser client sends, which keeps working while a Redfish job
// is queued; Redfish serves as the fallback.
func (s *Session) Power(ctx context.Context, action PowerAction) error {
	if err := s.ready(); err != nil {
		return err
	}
	operation, reset, err := powerMapping(action)
	if err != nil {
		return err
	}
	if err := s.sendRaw(PowerRequest(operation)); err != nil {
		s.logEvent("console power command failed, falling back to Redfish: %v", err)
		return s.client.Reset(ctx, reset)
	}
	return nil
}

func powerMapping(action PowerAction) (byte, ResetAction, error) {
	switch PowerAction(strings.ToLower(strings.TrimSpace(string(action)))) {
	case PowerMomentaryPress:
		return PowerGracefulShutdown, ResetGracefulShutdown, nil
	case PowerPressAndHold:
		return PowerOff, ResetForceOff, nil
	case PowerColdBootAction:
		return PowerColdBoot, ResetPowerCycle, nil
	case PowerResetAction:
		return PowerWarmBoot, ResetForceRestart, nil
	default:
		return 0, "", fmt.Errorf("unknown power action %q", action)
	}
}

// PowerOn turns a powered-off host on. The console channel offers it as its
// own operation because a momentary press does nothing on a running machine.
func (s *Session) PowerOn(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.sendRaw(PowerRequest(PowerOn)); err != nil {
		return s.client.Reset(ctx, ResetOn)
	}
	return nil
}

// Refresh repaints the whole screen. The browser client does the same thing
// with a plain non-incremental update request.
func (s *Session) Refresh() error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.rfb.RequestFullUpdate()
}

// ResetUSB re-enumerates the virtual keyboard and mouse on the host.
func (s *Session) ResetUSB() error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.sendRaw(USBReset())
}

// FocusLost tells the firmware the window lost keyboard focus so it drops any
// keys it still holds.
func (s *Session) FocusLost() error {
	if err := s.ready(); err != nil {
		return err
	}
	s.mu.Lock()
	s.keyboard.Reset()
	s.mu.Unlock()
	return s.sendRaw(FocusOut())
}

// ManagementStatus reads power state and boot override over Redfish.
func (s *Session) ManagementStatus(ctx context.Context) (ManagementStatus, error) {
	if !s.client.LoggedIn() {
		return ManagementStatus{}, errors.New("console is disconnected")
	}
	return s.client.GetManagementStatus(ctx)
}

// SetOneTimeBoot arms a one-time boot override.
func (s *Session) SetOneTimeBoot(ctx context.Context, device string) (BootOverrideChange, error) {
	if !s.client.LoggedIn() {
		return BootOverrideChange{}, errors.New("console is disconnected")
	}
	return s.client.SetOneTimeBoot(ctx, device)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
