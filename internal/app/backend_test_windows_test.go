//go:build windows

package app

import (
	"testing"

	"firstlight/internal/idrac"
	"firstlight/internal/kvm"
)

func TestPowerActionForCoversEveryOption(t *testing.T) {
	cases := []struct {
		option kvm.PowerOption
		want   idrac.PowerAction
	}{
		{kvm.PowerMomentaryPress, idrac.PowerMomentaryPress},
		{kvm.PowerPressAndHold, idrac.PowerPressAndHold},
		{kvm.PowerColdBoot, idrac.PowerColdBootAction},
		{kvm.PowerReset, idrac.PowerResetAction},
	}
	for _, tc := range cases {
		got, err := powerActionFor(tc.option)
		if err != nil {
			t.Fatalf("powerActionFor(%d): %v", tc.option, err)
		}
		if got != tc.want {
			t.Fatalf("powerActionFor(%d) = %q, want %q", tc.option, got, tc.want)
		}
	}
	if _, err := powerActionFor(kvm.PowerOption(200)); err == nil {
		t.Fatal("expected an error for an unknown power option")
	}
}

// The window drives both vendors through one interface, so a change to the
// keyboard or mouse signatures must break here rather than at runtime.
func TestBothBackendsSatisfyTheConsoleInterface(t *testing.T) {
	var _ consoleBackend = (*kvm.Conn)(nil)
	var _ consoleBackend = idracBackend{}
}
