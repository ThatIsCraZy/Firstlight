//go:build windows

package app

import (
	"errors"
	"fmt"
	"testing"

	"hpeirc/internal/kvm"
)

func TestDecodeServerUpdate(t *testing.T) {
	tests := []struct {
		name      string
		packet    kvm.CommandPacket
		wantOK    bool
		wantPower *bool
		wantPOST  string
	}{
		{name: "power on", packet: kvm.CommandPacket{Command: commandServerPower, Data: []byte{1}}, wantOK: true, wantPower: boolPointer(true)},
		{name: "power off", packet: kvm.CommandPacket{Command: commandServerPower, Data: []byte{0}}, wantOK: true, wantPower: boolPointer(false)},
		{name: "post byte order", packet: kvm.CommandPacket{Command: commandPOSTCode, Data: []byte{0x34, 0x12}}, wantOK: true, wantPOST: "1234"},
		{name: "short power", packet: kvm.CommandPacket{Command: commandServerPower}, wantOK: false},
		{name: "short post", packet: kvm.CommandPacket{Command: commandPOSTCode, Data: []byte{1}}, wantOK: false},
		{name: "unknown", packet: kvm.CommandPacket{Command: 99, Data: []byte{1, 2}}, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := decodeServerUpdate(test.packet)
			if ok != test.wantOK || got.postCode != test.wantPOST || !equalBoolPointers(got.power, test.wantPower) {
				t.Fatalf("decodeServerUpdate()=(%+v,%v), want power=%v post=%q ok=%v", got, ok, test.wantPower, test.wantPOST, test.wantOK)
			}
		})
	}
}

func TestIsTimeoutErrorRecognizesWrappedTimeout(t *testing.T) {
	if !isTimeoutError(fmt.Errorf("wrapped: %w", timeoutTestError{})) {
		t.Fatal("wrapped timeout was not recognized")
	}
	if isTimeoutError(errors.New("ordinary")) {
		t.Fatal("ordinary error was recognized as timeout")
	}
}

type timeoutTestError struct{}

func (timeoutTestError) Error() string { return "timeout" }
func (timeoutTestError) Timeout() bool { return true }

func boolPointer(value bool) *bool { return &value }

func equalBoolPointers(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
