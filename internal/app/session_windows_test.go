//go:build windows

package app

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"ilo-kvm/internal/ilo"
	"ilo-kvm/internal/kvm"
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

func TestNewKVMInfoMapsLegacyRCInfo(t *testing.T) {
	rc := &ilo.RCInfo{
		MasterKey:       []byte("0123456789abcdef"),
		LegacyKeyText:   "30313233343536373839616263646566",
		CommandKey:      []byte("fedcba9876543210"),
		ProtocolVersion: 1,
		OptionalFeatures: map[string]bool{
			"ENCRYPT_KEY":   true,
			"ENCRYPT_VMKEY": true,
			"ENCRYPT_CMD":   true,
		},
	}
	got := newKVMInfo("ilo4.example", 17990, "session", rc, kvm.ChannelKVM)
	if got.Legacy == nil {
		t.Fatal("legacy options were not created")
	}
	if !got.Legacy.EncryptSessionKey || !got.Legacy.EncryptVMKey || !got.Legacy.EncryptCommand {
		t.Fatalf("legacy feature mapping=%+v", got.Legacy)
	}
	if string(got.Legacy.EncryptionKey) != "0123456789abcdef" || string(got.Legacy.CommandKey) != "fedcba9876543210" {
		t.Fatalf("legacy key mapping=%+v", got.Legacy)
	}

	got.Legacy.EncryptionKey[0] = 'x'
	got.Legacy.CommandKey[0] = 'y'
	if rc.MasterKey[0] != '0' || rc.CommandKey[0] != 'f' {
		t.Fatal("newKVMInfo did not copy legacy key material")
	}
}

func TestDecodeLegacyShareRequest(t *testing.T) {
	data := make([]byte, 128)
	copy(data[:64], "OTHER-ADMIN")
	copy(data[64:], "192.0.2.44")
	got, err := decodeLegacyShareRequest(kvm.CommandPacket{Command: commandShareRequest, Flags: 15, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if got.User != "OTHER-ADMIN" || got.Address != "192.0.2.44" || got.Timeout != 15 {
		t.Fatalf("share request=%+v", got)
	}
}

func TestDecodeLegacyShareRequestRejectsMalformedPayload(t *testing.T) {
	if _, err := decodeLegacyShareRequest(kvm.CommandPacket{Command: commandShareRequest, Data: bytes.Repeat([]byte{0}, 127)}); err == nil {
		t.Fatal("expected short payload error")
	}
	data := make([]byte, 128)
	copy(data[64:], "not-an-ip")
	if _, err := decodeLegacyShareRequest(kvm.CommandPacket{Command: commandShareRequest, Data: data}); err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestLegacyShareDecisionTimeoutLeavesTimeForReverseDial(t *testing.T) {
	if got := legacyShareDecisionTimeout(0); got != 8*time.Second {
		t.Fatalf("zero timeout=%v", got)
	}
	if got := legacyShareDecisionTimeout(3); got != 3*time.Second {
		t.Fatalf("short timeout=%v", got)
	}
	if got := legacyShareDecisionTimeout(30); got != 8*time.Second {
		t.Fatalf("capped timeout=%v", got)
	}
}

type timeoutTestError struct{}

func (timeoutTestError) Error() string { return "timeout" }
func (timeoutTestError) Timeout() bool { return true }

func boolPointer(value bool) *bool { return &value }

func equalBoolPointers(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
