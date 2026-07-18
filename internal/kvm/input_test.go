package kvm

import "testing"

func TestMouseReportEncodesAbsoluteRelativeButtonsAndWheel(t *testing.T) {
	got := MouseReport(50, 100, -5, 200, 100, 200, -1, 3)
	want := [10]byte{2, 0, 0xdc, 0x05, 0xdc, 0x05, 0x85, 0x7f, 3, 0xff}
	if got != want {
		t.Fatalf("mouse report=%x want=%x", got, want)
	}
}

func TestMouseReportClampsCoordinatesAndInvalidDimensions(t *testing.T) {
	got := MouseReport(-10, 300, -500, 500, 100, 200, 0, 0)
	want := [10]byte{2, 0, 0, 0, 0xb8, 0x0b, 0xff, 0x7f, 0, 0}
	if got != want {
		t.Fatalf("clamped mouse report=%x want=%x", got, want)
	}
	got = MouseReport(1, 1, 0, 0, 0, 0, 0, 0)
	if got[2] != 0xb8 || got[3] != 0x0b || got[4] != 0xb8 || got[5] != 0x0b {
		t.Fatalf("invalid dimensions were not normalized: %x", got)
	}
}

func TestKeyboardReportUsesAtMostSixKeys(t *testing.T) {
	got := KeyboardReport(1, 4, 5, 6, 7, 8, 9, 10)
	want := [10]byte{1, 0, 1, 0, 4, 5, 6, 7, 8, 9}
	if got != want {
		t.Fatalf("keyboard report=%x want=%x", got, want)
	}
}
