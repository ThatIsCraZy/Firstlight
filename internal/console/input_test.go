package console

import (
	"reflect"
	"testing"
)

func TestParseChord(t *testing.T) {
	modifier, keys, err := parseChord([]string{"CTRL+ALT+DELETE"})
	if err != nil {
		t.Fatal(err)
	}
	if modifier != 5 {
		t.Fatalf("modifier = %d, want 5", modifier)
	}
	if !reflect.DeepEqual(keys, []byte{76}) {
		t.Fatalf("keys = %v, want [76]", keys)
	}
}

func TestParseChordRejectsUnknownKey(t *testing.T) {
	if _, _, err := parseChord([]string{"CTRL", "NOT_A_KEY"}); err == nil {
		t.Fatal("parseChord accepted an unknown key")
	}
}

func TestMouseButtonMask(t *testing.T) {
	tests := map[string]byte{"": 0, "left": 1, "RIGHT": 2, " middle ": 4}
	for input, want := range tests {
		got, err := mouseButtonMask(input)
		if err != nil {
			t.Fatalf("mouseButtonMask(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("mouseButtonMask(%q) = %d, want %d", input, got, want)
		}
	}
}
