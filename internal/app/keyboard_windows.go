//go:build windows

package app

import (
	"context"
	"time"

	"github.com/lxn/walk"

	"hpeirc/internal/keyboardmap"
	"hpeirc/internal/kvm"
)

type keyboardReportSender interface {
	SendAllKeysUp() error
	SendKeyboardReport([10]byte) error
}

func (w *appWindow) updateKeyboardRepeat() {
	if w.pressed[walk.KeyBack] {
		now := time.Now()
		if w.nextBackspace.IsZero() {
			w.nextBackspace = now.Add(350 * time.Millisecond)
		} else if !now.Before(w.nextBackspace) {
			w.sendKeyboard()
			w.nextBackspace = now.Add(45 * time.Millisecond)
		}
	}
}

func (w *appWindow) sendKeyboard() {
	w.mu.Lock()
	conn := w.conn
	ready := w.inputReady && w.captured
	w.mu.Unlock()
	if conn == nil || !ready {
		return
	}
	report := w.keyboardReport()
	if report == w.lastKeyReport {
		return
	}
	w.lastKeyReport = report
	if !w.pressed[walk.KeyBack] {
		w.nextBackspace = time.Time{}
	}
	if w.pressed[walk.KeyControl] && w.pressed[walk.KeyAlt] && w.pressed[walk.KeyDelete] {
		w.logf("tx keyboard ctrl-alt-del")
		_ = conn.SendCtrlAltDel()
		return
	}
	w.logf("tx keyboard report=%x", report)
	if err := conn.SendKeyboardReport(report); err != nil {
		w.logf("tx keyboard report error: %v", err)
	}
}

func (w *appWindow) keyboardReport() [10]byte {
	w.mu.Lock()
	layout := w.keyboardLayout
	rawInput := w.rawInput
	rawPressed := w.rawPressed
	if layout == keyboardLayoutDefault && rawInput {
		w.mu.Unlock()
		return hpKeyboardReport(rawPressed)
	}
	w.syncPressedModifiersLocked()
	w.mu.Unlock()
	return keyboardReportForRegistry(w.keyboardMaps, layout, w.pressed)
}

func keyboardReportForLayout(layout keyboardLayout, pressed map[walk.Key]bool) [10]byte {
	return keyboardReportForRegistry(keyboardmap.BuiltInRegistry(), layout, pressed)
}

func keyboardReportForRegistry(registry *keyboardmap.Registry, layout keyboardLayout, pressed map[walk.Key]bool) [10]byte {
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
		if pressed[walk.KeyShift] || pressed[walk.KeyLShift] || pressed[walk.KeyRShift] {
			state = keyboardmap.StateShift
		}
		return mappedKeyboardReport(registry, string(layout), state, pressed)
	}
	return defaultKeyboardReport(pressed)
}

func defaultKeyboardReport(pressed map[walk.Key]bool) [10]byte {
	mod := byte(0)
	shift := pressed[walk.KeyLShift] || pressed[walk.KeyRShift] || pressed[walk.KeyShift]
	if pressed[walk.KeyLControl] || pressed[walk.KeyControl] {
		mod |= 1
	}
	if shift {
		mod |= 2
	}
	if pressed[walk.KeyLAlt] || pressed[walk.KeyAlt] {
		mod |= 4
	}
	if pressed[walk.KeyLWin] {
		mod |= 8
	}
	if pressed[walk.KeyRControl] {
		mod |= 16
	}
	if pressed[walk.KeyRShift] {
		mod |= 32
	}
	if pressed[walk.KeyRAlt] {
		mod |= 64
	}
	if pressed[walk.KeyRWin] {
		mod |= 128
	}
	var keys []byte
	for key, definition := range walkKeys {
		if !pressed[key] {
			continue
		}
		hid := definition.hid
		if definition.physicalHID != 0 {
			hid = definition.physicalHID
		}
		if shift && key == walk.KeyOEMMinus {
			hid = 56
		}
		keys = append(keys, hid)
	}
	return kvm.KeyboardReport(mod, keys...)
}

func mappedKeyboardReport(registry *keyboardmap.Registry, mapID string, state keyboardmap.State, pressed map[walk.Key]bool) [10]byte {
	mod := byte(0)
	var keys []byte
	for key, isDown := range pressed {
		if !isDown || isModifierKey(key) {
			continue
		}
		definition, hasFallback := walkKeys[key]
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

func resolveMappedStroke(registry *keyboardmap.Registry, mapID string, key walk.Key, state keyboardmap.State) (keyboardmap.Stroke, bool) {
	if stroke, ok := registry.ResolvePhysical(mapID, keyboardmap.VKName(uint32(key)), state); ok {
		return stroke, true
	}
	if definition, ok := walkKeys[key]; ok {
		return registry.ResolvePhysical(mapID, definition.input, state)
	}
	return keyboardmap.Stroke{}, false
}

func hasSemanticModifier(pressed map[walk.Key]bool, altGr bool) bool {
	if pressed[walk.KeyLControl] || pressed[walk.KeyRControl] || pressed[walk.KeyLAlt] || pressed[walk.KeyLWin] || pressed[walk.KeyRWin] {
		if altGr && (pressed[walk.KeyRAlt] || pressed[walk.KeyLControl]) {
			return false
		}
		return true
	}
	return (pressed[walk.KeyControl] || pressed[walk.KeyAlt]) && !altGr
}

func isModifierKey(key walk.Key) bool {
	switch key {
	case walk.KeyShift, walk.KeyLShift, walk.KeyRShift,
		walk.KeyControl, walk.KeyLControl, walk.KeyRControl,
		walk.KeyAlt, walk.KeyLAlt, walk.KeyRAlt,
		walk.KeyLWin, walk.KeyRWin:
		return true
	default:
		return false
	}
}

func isAltGrCombo(registry *keyboardmap.Registry, mapID string, pressed map[walk.Key]bool) bool {
	if pressed[walk.KeyRAlt] {
		return hasPressedAltGrMappedKey(registry, mapID, pressed)
	}
	ctrl := pressed[walk.KeyControl] || pressed[walk.KeyLControl] || pressed[walk.KeyRControl]
	alt := pressed[walk.KeyAlt] || pressed[walk.KeyLAlt]
	return ctrl && alt && hasPressedAltGrMappedKey(registry, mapID, pressed)
}

func hasPressedAltGrMappedKey(registry *keyboardmap.Registry, mapID string, pressed map[walk.Key]bool) bool {
	for key, isDown := range pressed {
		if !isDown {
			continue
		}
		if registry.HasPhysical(mapID, keyboardmap.VKName(uint32(key)), keyboardmap.StateAltGr) {
			return true
		}
		if definition, ok := walkKeys[key]; ok && registry.HasPhysical(mapID, definition.input, keyboardmap.StateAltGr) {
			return true
		}
	}
	return false
}

func hasPressedNonModifierKey(pressed map[walk.Key]bool) bool {
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

// walkKeyDefinition is the single source of truth between Walk virtual keys,
// JSON input names, and the fallback US HID code. physicalHID is only set for
// the historic Default-layout position swap.
type walkKeyDefinition struct {
	input       string
	hid         byte
	physicalHID byte
}

var walkKeys = map[walk.Key]walkKeyDefinition{
	walk.KeyA: {input: "A", hid: 4}, walk.KeyB: {input: "B", hid: 5}, walk.KeyC: {input: "C", hid: 6},
	walk.KeyD: {input: "D", hid: 7}, walk.KeyE: {input: "E", hid: 8}, walk.KeyF: {input: "F", hid: 9},
	walk.KeyG: {input: "G", hid: 10}, walk.KeyH: {input: "H", hid: 11}, walk.KeyI: {input: "I", hid: 12},
	walk.KeyJ: {input: "J", hid: 13}, walk.KeyK: {input: "K", hid: 14}, walk.KeyL: {input: "L", hid: 15},
	walk.KeyM: {input: "M", hid: 16}, walk.KeyN: {input: "N", hid: 17}, walk.KeyO: {input: "O", hid: 18},
	walk.KeyP: {input: "P", hid: 19}, walk.KeyQ: {input: "Q", hid: 20}, walk.KeyR: {input: "R", hid: 21},
	walk.KeyS: {input: "S", hid: 22}, walk.KeyT: {input: "T", hid: 23}, walk.KeyU: {input: "U", hid: 24},
	walk.KeyV: {input: "V", hid: 25}, walk.KeyW: {input: "W", hid: 26}, walk.KeyX: {input: "X", hid: 27},
	walk.KeyY: {input: "Y", hid: 28, physicalHID: 29}, walk.KeyZ: {input: "Z", hid: 29, physicalHID: 28},
	walk.Key1: {input: "1", hid: 30}, walk.Key2: {input: "2", hid: 31}, walk.Key3: {input: "3", hid: 32},
	walk.Key4: {input: "4", hid: 33}, walk.Key5: {input: "5", hid: 34}, walk.Key6: {input: "6", hid: 35},
	walk.Key7: {input: "7", hid: 36}, walk.Key8: {input: "8", hid: 37}, walk.Key9: {input: "9", hid: 38},
	walk.Key0: {input: "0", hid: 39}, walk.KeyReturn: {input: "ENTER", hid: 40}, walk.KeyEscape: {input: "ESCAPE", hid: 41},
	walk.KeyBack: {input: "BACKSPACE", hid: 42}, walk.KeyTab: {input: "TAB", hid: 43}, walk.KeySpace: {input: "SPACE", hid: 44},
	walk.KeyOEMMinus: {input: "OEM_MINUS", hid: 45}, walk.KeyOEMPlus: {input: "OEM_PLUS", hid: 46},
	walk.KeyOEM4: {input: "OEM_4", hid: 47}, walk.KeyOEM6: {input: "OEM_6", hid: 48}, walk.KeyOEM5: {input: "OEM_5", hid: 49},
	walk.KeyOEM1: {input: "OEM_1", hid: 51}, walk.KeyOEM7: {input: "OEM_7", hid: 52}, walk.KeyOEM3: {input: "OEM_3", hid: 53},
	walk.KeyOEMComma: {input: "OEM_COMMA", hid: 54}, walk.KeyOEMPeriod: {input: "OEM_PERIOD", hid: 55}, walk.KeyOEM2: {input: "OEM_2", hid: 56},
	walk.KeyCapital: {input: "CAPS_LOCK", hid: 57}, walk.KeyF1: {input: "F1", hid: 58}, walk.KeyF2: {input: "F2", hid: 59},
	walk.KeyF3: {input: "F3", hid: 60}, walk.KeyF4: {input: "F4", hid: 61}, walk.KeyF5: {input: "F5", hid: 62},
	walk.KeyF6: {input: "F6", hid: 63}, walk.KeyF7: {input: "F7", hid: 64}, walk.KeyF8: {input: "F8", hid: 65},
	walk.KeyF9: {input: "F9", hid: 66}, walk.KeyF10: {input: "F10", hid: 67}, walk.KeyF11: {input: "F11", hid: 68}, walk.KeyF12: {input: "F12", hid: 69},
	walk.KeySnapshot: {input: "PRINT_SCREEN", hid: 70}, walk.KeyScroll: {input: "SCROLL_LOCK", hid: 71}, walk.KeyPause: {input: "PAUSE", hid: 72},
	walk.KeyInsert: {input: "INSERT", hid: 73}, walk.KeyHome: {input: "HOME", hid: 74}, walk.KeyPrior: {input: "PAGE_UP", hid: 75},
	walk.KeyDelete: {input: "DELETE", hid: 76}, walk.KeyEnd: {input: "END", hid: 77}, walk.KeyNext: {input: "PAGE_DOWN", hid: 78},
	walk.KeyRight: {input: "RIGHT", hid: 79}, walk.KeyLeft: {input: "LEFT", hid: 80}, walk.KeyDown: {input: "DOWN", hid: 81}, walk.KeyUp: {input: "UP", hid: 82},
	walk.KeyNumlock: {input: "NUM_LOCK", hid: 83}, walk.KeyDivide: {input: "KEYPAD_DIVIDE", hid: 84}, walk.KeyMultiply: {input: "KEYPAD_MULTIPLY", hid: 85},
	walk.KeySubtract: {input: "KEYPAD_SUBTRACT", hid: 86}, walk.KeyAdd: {input: "KEYPAD_ADD", hid: 87},
	walk.KeyNumpad1: {input: "KEYPAD_1", hid: 89}, walk.KeyNumpad2: {input: "KEYPAD_2", hid: 90}, walk.KeyNumpad3: {input: "KEYPAD_3", hid: 91},
	walk.KeyNumpad4: {input: "KEYPAD_4", hid: 92}, walk.KeyNumpad5: {input: "KEYPAD_5", hid: 93}, walk.KeyNumpad6: {input: "KEYPAD_6", hid: 94},
	walk.KeyNumpad7: {input: "KEYPAD_7", hid: 95}, walk.KeyNumpad8: {input: "KEYPAD_8", hid: 96}, walk.KeyNumpad9: {input: "KEYPAD_9", hid: 97},
	walk.KeyNumpad0: {input: "KEYPAD_0", hid: 98}, walk.KeyDecimal: {input: "KEYPAD_DECIMAL", hid: 99},
	walk.KeyOEM102: {input: "OEM_102", hid: 100}, walk.KeyApps: {input: "APPLICATION", hid: 101},
}
