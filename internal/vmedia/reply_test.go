package vmedia

import "testing"

func TestReplyHeaderBytes(t *testing.T) {
	h := ReplyHeader{Flags: flagKeepalive, Media: 1}.WithSense(5, 36, 0, 2048).Bytes()
	want := [16]byte{0xde, 0xc0, 0xad, 0x0b, 2, 0, 0, 0, 1, 5, 36, 0, 0, 8, 0, 0}
	if h != want {
		t.Fatalf("header=%x want=%x", h, want)
	}
}

func TestSyncHeaderBytes(t *testing.T) {
	got := SyncHeader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	if got[0] != 0xde || got[1] != 0xc0 || got[4] != flagKeepalive || got[8] != 1 || got[15] != 8 {
		t.Fatalf("bad sync header: %x", got)
	}
}
