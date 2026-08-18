package kvm

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestKDFStable(t *testing.T) {
	kdf := NewKDF([]byte("0123456789abcdef"))
	got := make([]byte, 32)
	kdf.Derive(got)
	const want = "ceb3bd2cfc102433aeedbfb9d0f7c2c93a6e6e9a395bebd6ab035ee49102caff"
	if hex.EncodeToString(got) != want {
		t.Fatalf("derive mismatch:\n got %x\nwant %s", got, want)
	}
}

func TestDeriveKeyPairSkipMatchesSequentialKDF(t *testing.T) {
	master := []byte("0123456789abcdef")
	kdf := NewKDF(master)
	discard := make([]byte, 64)
	kdf.Derive(discard)
	wantIn := make([]byte, 16)
	wantOut := make([]byte, 16)
	kdf.Derive(wantIn)
	kdf.Derive(wantOut)

	gotIn, gotOut := DeriveKeyPair(master, 2)
	if !bytes.Equal(gotIn, wantIn) || !bytes.Equal(gotOut, wantOut) {
		t.Fatalf("skip pair mismatch:\n got in=%x out=%x\nwant in=%x out=%x", gotIn, gotOut, wantIn, wantOut)
	}
}

func TestAESStreamRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("abcdef9876543210")
	a, err := NewAESStream(key, iv)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewAESStream(key, iv)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello ilo remote console")
	wire := append([]byte(nil), msg...)
	a.XORKeyStream(wire, wire)
	if bytes.Equal(wire, msg) {
		t.Fatal("stream did not transform data")
	}
	b.XORKeyStream(wire, wire)
	if !bytes.Equal(wire, msg) {
		t.Fatalf("roundtrip mismatch: %q", wire)
	}
}

func TestClientHelloLayout(t *testing.T) {
	hello := NewClientHello([]byte("1234567890abcdef"), CommandShare, ChannelKVM, "abc")
	wire := hello.MarshalBinary()
	if len(wire) != 54 {
		t.Fatalf("len=%d", len(wire))
	}
	if wire[16] != byte(CommandShare) || wire[17] != byte(ChannelKVM) {
		t.Fatalf("bad command/channel: %x", wire[16:18])
	}
	if string(wire[22:25]) != "abc" {
		t.Fatalf("bad session key: %x", wire[22:54])
	}
}

func TestClientHelloDiscChannelLayout(t *testing.T) {
	hello := NewClientHello([]byte("1234567890abcdef"), CommandNew, ChannelDisc, "abc")
	wire := hello.MarshalBinary()
	if wire[16] != byte(CommandNew) || wire[17] != 3 {
		t.Fatalf("bad disc command/channel: %x", wire[16:18])
	}
}

func TestKeyboardReportLayout(t *testing.T) {
	report := KeyboardReport(0x01, 0x06, 0x28)
	want := [10]byte{1, 0, 0x01, 0, 0x06, 0x28, 0, 0, 0, 0}
	if report != want {
		t.Fatalf("got %x want %x", report, want)
	}
}
