//go:build windows

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lxn/walk"

	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
)

func TestWalkKeyDefinitionsMatchUSBase(t *testing.T) {
	registry := keyboardmap.BuiltInRegistry()
	seenInputs := make(map[string]walk.Key, len(walkKeys))
	for key, definition := range walkKeys {
		if definition.input == "" || definition.hid == 0 {
			t.Fatalf("key %#x has incomplete definition: %+v", key, definition)
		}
		if previous, exists := seenInputs[definition.input]; exists {
			t.Fatalf("input %q is assigned to both %#x and %#x", definition.input, previous, key)
		}
		seenInputs[definition.input] = key

		stroke, ok := registry.ResolvePhysical("us-base", definition.input, keyboardmap.StatePlain)
		if !ok || stroke.Suppress || stroke.Key != definition.hid || stroke.Modifiers != 0 {
			t.Fatalf("key %#x input %q fallback=%+v, us-base=%+v found=%v", key, definition.input, definition, stroke, ok)
		}
	}
}

func TestResetInputState(t *testing.T) {
	w := &appWindow{
		pressed:       keys(walk.KeyA, walk.KeyShift),
		rawInput:      true,
		lastKeyReport: kvm.KeyboardReport(2, 4),
		nextBackspace: time.Now(),
		mouseButtons:  7,
	}
	w.rawPressed[65] = true

	w.resetKeyboardStateLocked()
	if len(w.pressed) != 0 || w.rawInput || w.rawPressed[65] || w.lastKeyReport != kvm.KeyboardReport(0) || !w.nextBackspace.IsZero() {
		t.Fatalf("keyboard state was not fully reset: %+v", w)
	}
	if w.mouseButtons != 7 {
		t.Fatalf("keyboard-only reset changed mouse buttons to %d", w.mouseButtons)
	}

	w.resetCapturedInputLocked()
	if w.mouseButtons != 0 {
		t.Fatalf("captured input reset left mouse buttons=%d", w.mouseButtons)
	}
}

func TestSendClipboardTextReportOrderAndUnsupportedRunes(t *testing.T) {
	sender := &recordingKeyboardSender{}
	sent, skipped, err := sendClipboardTextWithDelay(
		context.Background(), sender, keyboardmap.BuiltInRegistry(), keyboardLayoutForceGerman, "Aä\r\n", 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || skipped != 1 {
		t.Fatalf("sent=%d skipped=%d want sent=2 skipped=1", sent, skipped)
	}
	want := [][10]byte{
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(2, 4),
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(0, 40),
		kvm.KeyboardReport(0),
		kvm.KeyboardReport(0),
	}
	if len(sender.reports) != len(want) {
		t.Fatalf("reports=%x want=%x", sender.reports, want)
	}
	for i := range want {
		if sender.reports[i] != want[i] {
			t.Fatalf("report[%d]=%x want=%x", i, sender.reports[i], want[i])
		}
	}
}

func TestSendClipboardTextHonorsCancellationAndSenderErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sender := &recordingKeyboardSender{}
	sent, skipped, err := sendClipboardTextWithDelay(ctx, sender, keyboardmap.BuiltInRegistry(), keyboardLayoutDefault, "a", 0)
	if !errors.Is(err, context.Canceled) || sent != 0 || skipped != 0 || len(sender.reports) != 1 {
		t.Fatalf("cancel result sent=%d skipped=%d err=%v reports=%d", sent, skipped, err, len(sender.reports))
	}

	wantErr := errors.New("send failed")
	failing := &recordingKeyboardSender{failAt: 2, err: wantErr}
	sent, skipped, err = sendClipboardTextWithDelay(context.Background(), failing, keyboardmap.BuiltInRegistry(), keyboardLayoutDefault, "a", 0)
	if !errors.Is(err, wantErr) || sent != 0 || skipped != 0 {
		t.Fatalf("sender failure sent=%d skipped=%d err=%v", sent, skipped, err)
	}
}

type recordingKeyboardSender struct {
	reports [][10]byte
	calls   int
	failAt  int
	err     error
}

func (s *recordingKeyboardSender) SendAllKeysUp() error {
	return s.record(kvm.KeyboardReport(0))
}

func (s *recordingKeyboardSender) SendKeyboardReport(report [10]byte) error {
	return s.record(report)
}

func (s *recordingKeyboardSender) record(report [10]byte) error {
	s.calls++
	if s.calls == s.failAt {
		return s.err
	}
	s.reports = append(s.reports, report)
	return nil
}
