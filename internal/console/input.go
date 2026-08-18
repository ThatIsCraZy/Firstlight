package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ilo-kvm/internal/kvm"
)

const defaultInputDelay = 8 * time.Millisecond

func (s *Session) TypeText(ctx context.Context, text, layout string, delay time.Duration) (TextResult, error) {
	if utf8.RuneCountInString(text) > MaximumTextRunes {
		return TextResult{}, fmt.Errorf("text exceeds maximum length of %d characters", MaximumTextRunes)
	}
	if delay < 0 || delay > time.Second {
		return TextResult{}, errors.New("text delay must be between 0 and 1000 milliseconds")
	}
	if delay == 0 {
		delay = defaultInputDelay
	}
	layout = strings.TrimSpace(layout)
	if layout == "" {
		layout = "us-base"
	}
	if _, ok := s.keyboardMaps.Info(layout); !ok {
		return TextResult{}, fmt.Errorf("unknown keyboard layout %q", layout)
	}

	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	conn, err := s.readyConnection(false)
	if err != nil {
		return TextResult{}, err
	}
	if err := conn.SendAllKeysUp(); err != nil {
		s.markDisconnected(err.Error())
		return TextResult{}, err
	}
	result := TextResult{}
	for _, char := range text {
		if err := ctx.Err(); err != nil {
			_ = conn.SendAllKeysUp()
			return result, err
		}
		if char == '\r' {
			continue
		}
		strokes, ok := s.keyboardMaps.ResolveText(layout, char)
		if !ok {
			result.Skipped++
			continue
		}
		for _, stroke := range strokes {
			if err := conn.SendKeyboardReport(kvm.KeyboardReport(stroke.Modifiers, stroke.Key)); err != nil {
				_ = conn.SendAllKeysUp()
				s.markDisconnected(err.Error())
				return result, err
			}
			if err := waitContext(ctx, delay); err != nil {
				_ = conn.SendAllKeysUp()
				return result, err
			}
			if err := conn.SendAllKeysUp(); err != nil {
				s.markDisconnected(err.Error())
				return result, err
			}
			if err := waitContext(ctx, delay); err != nil {
				return result, err
			}
		}
		result.Sent++
	}
	return result, conn.SendAllKeysUp()
}

func (s *Session) PressKeys(ctx context.Context, names []string, hold time.Duration) error {
	modifier, keys, err := parseChord(names)
	if err != nil {
		return err
	}
	if hold <= 0 {
		hold = 80 * time.Millisecond
	}
	if hold > 5*time.Second {
		return errors.New("key hold duration must not exceed 5000 milliseconds")
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	conn, err := s.readyConnection(false)
	if err != nil {
		return err
	}
	if err := conn.SendAllKeysUp(); err != nil {
		s.markDisconnected(err.Error())
		return err
	}
	if err := conn.SendKeyboardReport(kvm.KeyboardReport(modifier, keys...)); err != nil {
		s.markDisconnected(err.Error())
		return err
	}
	if err := waitContext(ctx, hold); err != nil {
		_ = conn.SendAllKeysUp()
		return err
	}
	if err := conn.SendAllKeysUp(); err != nil {
		s.markDisconnected(err.Error())
		return err
	}
	return nil
}

func (s *Session) Mouse(ctx context.Context, action string, x, y int, button string, wheel int8) error {
	action = strings.ToLower(strings.TrimSpace(action))
	button = strings.ToLower(strings.TrimSpace(button))
	if action == "" {
		action = "move"
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	conn, err := s.readyConnection(false)
	if err != nil {
		return err
	}
	s.mu.Lock()
	width, height := s.width, s.height
	previousX, previousY := s.mouseX, s.mouseY
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
	send := func(currentButtons byte, currentWheel int8) error {
		if err := conn.SendMouse(x, y, x-previousX, y-previousY, width, height, currentWheel, currentButtons); err != nil {
			s.markDisconnected(err.Error())
			return err
		}
		previousX, previousY = x, y
		s.mu.Lock()
		s.mouseX, s.mouseY, s.mouseButtons = x, y, currentButtons
		s.mu.Unlock()
		return nil
	}
	switch action {
	case "move":
		if button != "" {
			return errors.New("button is not valid for mouse move")
		}
		return send(buttons, wheel)
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

func (s *Session) Power(action string) error {
	var option kvm.PowerOption
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "momentary_press":
		option = kvm.PowerMomentaryPress
	case "press_and_hold":
		option = kvm.PowerPressAndHold
	case "cold_boot":
		option = kvm.PowerColdBoot
	case "reset":
		option = kvm.PowerReset
	default:
		return fmt.Errorf("unknown power action %q", action)
	}
	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	conn, err := s.readyConnection(true)
	if err != nil {
		return err
	}
	if err := conn.SendPower(option); err != nil {
		s.markDisconnected(err.Error())
		return err
	}
	return nil
}

func (s *Session) readyConnection(requireOwner bool) (*kvm.Conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.conn == nil {
		if s.disconnectReason != "" {
			return nil, fmt.Errorf("console is disconnected: %s", s.disconnectReason)
		}
		return nil, errors.New("console is disconnected")
	}
	if !s.inputReady {
		return nil, errors.New("console input is not ready; observe the console and retry")
	}
	if requireOwner && s.shared {
		return nil, errors.New("power controls are unavailable in a shared console session")
	}
	return s.conn, nil
}

func waitContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mouseButtonMask(name string) (byte, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return 0, nil
	case "left":
		return 1, nil
	case "right":
		return 2, nil
	case "middle":
		return 4, nil
	default:
		return 0, fmt.Errorf("unknown mouse button %q", name)
	}
}

func parseChord(names []string) (byte, []byte, error) {
	if len(names) == 0 {
		return 0, nil, errors.New("at least one key is required")
	}
	var modifier byte
	var keys []byte
	seen := make(map[byte]bool)
	for _, item := range names {
		for _, rawName := range strings.Split(item, "+") {
			name := strings.ToUpper(strings.TrimSpace(rawName))
			if name == "" {
				continue
			}
			if value, ok := chordModifiers[name]; ok {
				modifier |= value
				continue
			}
			key, ok := chordKeys[name]
			if !ok {
				return 0, nil, fmt.Errorf("unknown key %q", rawName)
			}
			if !seen[key] {
				keys = append(keys, key)
				seen[key] = true
			}
		}
	}
	if modifier == 0 && len(keys) == 0 {
		return 0, nil, errors.New("at least one key is required")
	}
	if len(keys) > 6 {
		return 0, nil, errors.New("a keyboard chord may contain at most six non-modifier keys")
	}
	return modifier, keys, nil
}

var chordModifiers = map[string]byte{
	"CTRL": 1, "CONTROL": 1, "LEFT_CTRL": 1, "LEFT_CONTROL": 1,
	"SHIFT": 2, "LEFT_SHIFT": 2,
	"ALT": 4, "LEFT_ALT": 4,
	"META": 8, "GUI": 8, "WINDOWS": 8, "LEFT_META": 8, "LEFT_GUI": 8,
	"RIGHT_CTRL": 16, "RIGHT_CONTROL": 16,
	"RIGHT_SHIFT": 32,
	"RIGHT_ALT":   64, "ALTGR": 64,
	"RIGHT_META": 128, "RIGHT_GUI": 128,
}

var chordKeys = buildChordKeys()

func buildChordKeys() map[string]byte {
	keys := map[string]byte{
		"ENTER": 40, "RETURN": 40, "ESC": 41, "ESCAPE": 41,
		"BACKSPACE": 42, "TAB": 43, "SPACE": 44, "CAPS_LOCK": 57,
		"PRINT_SCREEN": 70, "SCROLL_LOCK": 71, "PAUSE": 72,
		"INSERT": 73, "HOME": 74, "PAGE_UP": 75, "PGUP": 75,
		"DELETE": 76, "DEL": 76, "END": 77, "PAGE_DOWN": 78, "PGDN": 78,
		"RIGHT": 79, "ARROW_RIGHT": 79, "LEFT": 80, "ARROW_LEFT": 80,
		"DOWN": 81, "ARROW_DOWN": 81, "UP": 82, "ARROW_UP": 82,
		"NUM_LOCK": 83,
	}
	for i := byte(0); i < 26; i++ {
		keys[string(rune('A'+i))] = 4 + i
	}
	for i, name := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"} {
		keys[name] = byte(30 + i)
	}
	for i := 1; i <= 12; i++ {
		keys[fmt.Sprintf("F%d", i)] = byte(57 + i)
	}
	return keys
}
