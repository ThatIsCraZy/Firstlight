//go:build windows

package app

import (
	"context"
	"time"

	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
)

type keyboardReportSender interface {
	SendAllKeysUp() error
	SendKeyboardReport([10]byte) error
}

func (w *appWindow) updateKeyboardRepeat() {
	w.input.Lock()
	fire := false
	if w.pressed[KeyBack] {
		now := time.Now()
		if w.nextBackspace.IsZero() {
			w.nextBackspace = now.Add(350 * time.Millisecond)
		} else if !now.Before(w.nextBackspace) {
			fire = true
			w.nextBackspace = now.Add(45 * time.Millisecond)
		}
	}
	w.input.Unlock()
	if fire {
		w.sendKeyboard()
	}
}

func (w *appWindow) sendKeyboard() {
	w.mu.Lock()
	conn := w.conn
	ready := w.inputReady && w.captured
	layout := w.keyboardLayout
	w.mu.Unlock()
	if conn == nil || !ready {
		return
	}
	w.input.Lock()
	report := w.keyboardReportLocked(layout)
	if report == w.lastKeyReport {
		w.input.Unlock()
		return
	}
	w.lastKeyReport = report
	if !w.pressed[KeyBack] {
		w.nextBackspace = time.Time{}
	}
	ctrlAltDel := w.pressed[KeyControl] && w.pressed[KeyAlt] && w.pressed[KeyDelete]
	w.input.Unlock()
	if ctrlAltDel {
		w.logf("tx keyboard ctrl-alt-del")
		_ = conn.SendCtrlAltDel()
		return
	}
	w.logf("tx keyboard report=%x", report)
	if err := conn.SendKeyboardReport(report); err != nil {
		w.logf("tx keyboard report error: %v", err)
	}
}

// keyboardReportLocked requires w.input to be held.
func (w *appWindow) keyboardReportLocked(layout keyboardLayout) [10]byte {
	if layout == keyboardLayoutDefault && w.rawInput {
		return hpKeyboardReport(w.rawPressed)
	}
	w.syncPressedModifiersLocked()
	return keyboardReportForRegistry(w.keyboardMaps, layout, w.pressed)
}

func keyboardReportForLayout(layout keyboardLayout, pressed map[Key]bool) [10]byte {
	return keyboardReportForRegistry(keyboardmap.BuiltInRegistry(), layout, pressed)
}

func keyboardReportForRegistry(registry *keyboardmap.Registry, layout keyboardLayout, pressed map[Key]bool) [10]byte {
	if !hasPressedNonModifierKey(pressed) {
		return kvm.KeyboardReport(0)
	}
	mapID := string(layout)
	if layout == keyboardLayoutDefault {
		// Preserve the established semantic AltGr behavior. The live Default path
		// still uses hpKeyboardReport and remains physical pass-through.
		mapID = string(keyboardLayoutForceGerman)
	}
	altGr := isAltGrCombo(registry, mapID, pressed)
	if altGr {
		return mappedKeyboardReport(registry, mapID, keyboardmap.StateAltGr, pressed)
	}
	if layout != keyboardLayoutDefault && !hasSemanticModifier(pressed, altGr) {
		state := keyboardmap.StatePlain
		if pressed[KeyShift] || pressed[KeyLShift] || pressed[KeyRShift] {
			state = keyboardmap.StateShift
		}
		return mappedKeyboardReport(registry, string(layout), state, pressed)
	}
	return defaultKeyboardReport(pressed)
}

func defaultKeyboardReport(pressed map[Key]bool) [10]byte {
	mod := byte(0)
	shift := pressed[KeyLShift] || pressed[KeyRShift] || pressed[KeyShift]
	if pressed[KeyLControl] || pressed[KeyControl] {
		mod |= 1
	}
	if shift {
		mod |= 2
	}
	if pressed[KeyLAlt] || pressed[KeyAlt] {
		mod |= 4
	}
	if pressed[KeyLWin] {
		mod |= 8
	}
	if pressed[KeyRControl] {
		mod |= 16
	}
	if pressed[KeyRShift] {
		mod |= 32
	}
	if pressed[KeyRAlt] {
		mod |= 64
	}
	if pressed[KeyRWin] {
		mod |= 128
	}
	var keys []byte
	for key, definition := range vkKeys {
		if !pressed[key] {
			continue
		}
		hid := definition.hid
		if definition.physicalHID != 0 {
			hid = definition.physicalHID
		}
		if shift && key == KeyOEMMinus {
			hid = 56
		}
		keys = append(keys, hid)
	}
	return kvm.KeyboardReport(mod, keys...)
}

func mappedKeyboardReport(registry *keyboardmap.Registry, mapID string, state keyboardmap.State, pressed map[Key]bool) [10]byte {
	mod := byte(0)
	var keys []byte
	for key, isDown := range pressed {
		if !isDown || isModifierKey(key) {
			continue
		}
		definition, hasFallback := vkKeys[key]
		hid := definition.hid
		stroke, ok := resolveMappedStroke(registry, mapID, key, state)
		if ok {
			if stroke.Suppress {
				continue
			}
			hid = stroke.Key
			mod |= stroke.Modifiers
		} else if !hasFallback {
			continue
		} else if state == keyboardmap.StateShift {
			mod |= 2
		} else if state == keyboardmap.StateAltGr {
			mod |= 0x50
		}
		keys = append(keys, hid)
	}
	return kvm.KeyboardReport(mod, keys...)
}

func resolveMappedStroke(registry *keyboardmap.Registry, mapID string, key Key, state keyboardmap.State) (keyboardmap.Stroke, bool) {
	if stroke, ok := registry.ResolvePhysical(mapID, keyboardmap.VKName(uint32(key)), state); ok {
		return stroke, true
	}
	if definition, ok := vkKeys[key]; ok {
		return registry.ResolvePhysical(mapID, definition.input, state)
	}
	return keyboardmap.Stroke{}, false
}

func hasSemanticModifier(pressed map[Key]bool, altGr bool) bool {
	if pressed[KeyLControl] || pressed[KeyRControl] || pressed[KeyLAlt] || pressed[KeyLWin] || pressed[KeyRWin] {
		if altGr && (pressed[KeyRAlt] || pressed[KeyLControl]) {
			return false
		}
		return true
	}
	return (pressed[KeyControl] || pressed[KeyAlt]) && !altGr
}

func isModifierKey(key Key) bool {
	switch key {
	case KeyShift, KeyLShift, KeyRShift,
		KeyControl, KeyLControl, KeyRControl,
		KeyAlt, KeyLAlt, KeyRAlt,
		KeyLWin, KeyRWin:
		return true
	default:
		return false
	}
}

func isAltGrCombo(registry *keyboardmap.Registry, mapID string, pressed map[Key]bool) bool {
	if pressed[KeyRAlt] {
		return hasPressedAltGrMappedKey(registry, mapID, pressed)
	}
	ctrl := pressed[KeyControl] || pressed[KeyLControl] || pressed[KeyRControl]
	alt := pressed[KeyAlt] || pressed[KeyLAlt]
	return ctrl && alt && hasPressedAltGrMappedKey(registry, mapID, pressed)
}

func hasPressedAltGrMappedKey(registry *keyboardmap.Registry, mapID string, pressed map[Key]bool) bool {
	for key, isDown := range pressed {
		if !isDown {
			continue
		}
		if registry.HasPhysical(mapID, keyboardmap.VKName(uint32(key)), keyboardmap.StateAltGr) {
			return true
		}
		if definition, ok := vkKeys[key]; ok && registry.HasPhysical(mapID, definition.input, keyboardmap.StateAltGr) {
			return true
		}
	}
	return false
}

func hasPressedNonModifierKey(pressed map[Key]bool) bool {
	for key, isDown := range pressed {
		if isDown && !isModifierKey(key) {
			return true
		}
	}
	return false
}

func clipboardReportForRune(layout keyboardLayout, r rune) ([10]byte, bool) {
	strokes, ok := clipboardStrokesForRune(keyboardmap.BuiltInRegistry(), layout, r)
	if !ok || len(strokes) != 1 {
		return [10]byte{}, false
	}
	return kvm.KeyboardReport(strokes[0].Modifiers, strokes[0].Key), true
}

func clipboardStrokesForRune(registry *keyboardmap.Registry, layout keyboardLayout, r rune) ([]keyboardmap.Stroke, bool) {
	return registry.ResolveText(string(layout), r)
}

func sendClipboardText(ctx context.Context, sender keyboardReportSender, registry *keyboardmap.Registry, layout keyboardLayout, text string) (int, int, error) {
	return sendClipboardTextWithDelay(ctx, sender, registry, layout, text, 8*time.Millisecond)
}

func sendClipboardTextWithDelay(ctx context.Context, sender keyboardReportSender, registry *keyboardmap.Registry, layout keyboardLayout, text string, delay time.Duration) (int, int, error) {
	if err := sender.SendAllKeysUp(); err != nil {
		return 0, 0, err
	}
	sent, skipped := 0, 0
	for _, r := range text {
		select {
		case <-ctx.Done():
			return sent, skipped, ctx.Err()
		default:
		}
		if r == '\r' {
			continue
		}
		strokes, ok := clipboardStrokesForRune(registry, layout, r)
		if !ok {
			skipped++
			continue
		}
		for _, stroke := range strokes {
			if err := sender.SendKeyboardReport(kvm.KeyboardReport(stroke.Modifiers, stroke.Key)); err != nil {
				return sent, skipped, err
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			if err := sender.SendAllKeysUp(); err != nil {
				return sent, skipped, err
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		sent++
	}
	return sent, skipped, sender.SendAllKeysUp()
}

// vkKeyDefinition is the single source of truth between Walk virtual keys,
// JSON input names, and the fallback US HID code. physicalHID is only set for
// the historic Default-layout position swap.
type vkKeyDefinition struct {
	input       string
	hid         byte
	physicalHID byte
}

var vkKeys = map[Key]vkKeyDefinition{
	KeyA: {input: "A", hid: 4}, KeyB: {input: "B", hid: 5}, KeyC: {input: "C", hid: 6},
	KeyD: {input: "D", hid: 7}, KeyE: {input: "E", hid: 8}, KeyF: {input: "F", hid: 9},
	KeyG: {input: "G", hid: 10}, KeyH: {input: "H", hid: 11}, KeyI: {input: "I", hid: 12},
	KeyJ: {input: "J", hid: 13}, KeyK: {input: "K", hid: 14}, KeyL: {input: "L", hid: 15},
	KeyM: {input: "M", hid: 16}, KeyN: {input: "N", hid: 17}, KeyO: {input: "O", hid: 18},
	KeyP: {input: "P", hid: 19}, KeyQ: {input: "Q", hid: 20}, KeyR: {input: "R", hid: 21},
	KeyS: {input: "S", hid: 22}, KeyT: {input: "T", hid: 23}, KeyU: {input: "U", hid: 24},
	KeyV: {input: "V", hid: 25}, KeyW: {input: "W", hid: 26}, KeyX: {input: "X", hid: 27},
	KeyY: {input: "Y", hid: 28, physicalHID: 29}, KeyZ: {input: "Z", hid: 29, physicalHID: 28},
	Key1: {input: "1", hid: 30}, Key2: {input: "2", hid: 31}, Key3: {input: "3", hid: 32},
	Key4: {input: "4", hid: 33}, Key5: {input: "5", hid: 34}, Key6: {input: "6", hid: 35},
	Key7: {input: "7", hid: 36}, Key8: {input: "8", hid: 37}, Key9: {input: "9", hid: 38},
	Key0: {input: "0", hid: 39}, KeyReturn: {input: "ENTER", hid: 40}, KeyEscape: {input: "ESCAPE", hid: 41},
	KeyBack: {input: "BACKSPACE", hid: 42}, KeyTab: {input: "TAB", hid: 43}, KeySpace: {input: "SPACE", hid: 44},
	KeyOEMMinus: {input: "OEM_MINUS", hid: 45}, KeyOEMPlus: {input: "OEM_PLUS", hid: 46},
	KeyOEM4: {input: "OEM_4", hid: 47}, KeyOEM6: {input: "OEM_6", hid: 48}, KeyOEM5: {input: "OEM_5", hid: 49},
	KeyOEM1: {input: "OEM_1", hid: 51}, KeyOEM7: {input: "OEM_7", hid: 52}, KeyOEM3: {input: "OEM_3", hid: 53},
	KeyOEMComma: {input: "OEM_COMMA", hid: 54}, KeyOEMPeriod: {input: "OEM_PERIOD", hid: 55}, KeyOEM2: {input: "OEM_2", hid: 56},
	KeyCapital: {input: "CAPS_LOCK", hid: 57}, KeyF1: {input: "F1", hid: 58}, KeyF2: {input: "F2", hid: 59},
	KeyF3: {input: "F3", hid: 60}, KeyF4: {input: "F4", hid: 61}, KeyF5: {input: "F5", hid: 62},
	KeyF6: {input: "F6", hid: 63}, KeyF7: {input: "F7", hid: 64}, KeyF8: {input: "F8", hid: 65},
	KeyF9: {input: "F9", hid: 66}, KeyF10: {input: "F10", hid: 67}, KeyF11: {input: "F11", hid: 68}, KeyF12: {input: "F12", hid: 69},
	KeySnapshot: {input: "PRINT_SCREEN", hid: 70}, KeyScroll: {input: "SCROLL_LOCK", hid: 71}, KeyPause: {input: "PAUSE", hid: 72},
	KeyInsert: {input: "INSERT", hid: 73}, KeyHome: {input: "HOME", hid: 74}, KeyPrior: {input: "PAGE_UP", hid: 75},
	KeyDelete: {input: "DELETE", hid: 76}, KeyEnd: {input: "END", hid: 77}, KeyNext: {input: "PAGE_DOWN", hid: 78},
	KeyRight: {input: "RIGHT", hid: 79}, KeyLeft: {input: "LEFT", hid: 80}, KeyDown: {input: "DOWN", hid: 81}, KeyUp: {input: "UP", hid: 82},
	KeyNumlock: {input: "NUM_LOCK", hid: 83}, KeyDivide: {input: "KEYPAD_DIVIDE", hid: 84}, KeyMultiply: {input: "KEYPAD_MULTIPLY", hid: 85},
	KeySubtract: {input: "KEYPAD_SUBTRACT", hid: 86}, KeyAdd: {input: "KEYPAD_ADD", hid: 87},
	KeyNumpad1: {input: "KEYPAD_1", hid: 89}, KeyNumpad2: {input: "KEYPAD_2", hid: 90}, KeyNumpad3: {input: "KEYPAD_3", hid: 91},
	KeyNumpad4: {input: "KEYPAD_4", hid: 92}, KeyNumpad5: {input: "KEYPAD_5", hid: 93}, KeyNumpad6: {input: "KEYPAD_6", hid: 94},
	KeyNumpad7: {input: "KEYPAD_7", hid: 95}, KeyNumpad8: {input: "KEYPAD_8", hid: 96}, KeyNumpad9: {input: "KEYPAD_9", hid: 97},
	KeyNumpad0: {input: "KEYPAD_0", hid: 98}, KeyDecimal: {input: "KEYPAD_DECIMAL", hid: 99},
	KeyOEM102: {input: "OEM_102", hid: 100}, KeyApps: {input: "APPLICATION", hid: 101},
}
