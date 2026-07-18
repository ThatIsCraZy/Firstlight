package kvm

import (
	"bytes"
	"testing"
)

func TestReadCommandPacket(t *testing.T) {
	got, err := readCommandPacket(bytes.NewReader([]byte{
		3, 0, 0, 0,
		1, 0, 0, 0,
		7, 0,
		9, 0,
		1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != 3 || got.Size != 1 || got.Seq != 7 || got.Flags != 9 || len(got.Data) != 1 || got.Data[0] != 1 {
		t.Fatalf("unexpected packet: %#v", got)
	}
}

func TestReadCommandPacketRejectsLargePayload(t *testing.T) {
	_, err := readCommandPacket(bytes.NewReader([]byte{
		5, 0, 0, 0,
		0xdd, 0x05, 0, 0,
		0, 0,
		0, 0,
	}))
	if err == nil {
		t.Fatal("expected error")
	}
}
