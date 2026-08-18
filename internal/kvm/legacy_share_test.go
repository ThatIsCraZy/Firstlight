package kvm

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestLegacyShareLeaderRelaysVideoInputAndPeerEvents(t *testing.T) {
	leaderClient, iloSide := net.Pipe()
	defer iloSide.Close()
	leaderConn := &Conn{net: leaderClient}
	hub := NewLegacyShareLeader(leaderConn)
	defer hub.Close()

	hubSide, peerSide := net.Pipe()
	defer peerSide.Close()
	if err := hub.addPeerConn(hubSide, net.ParseIP("192.0.2.25")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	_ = iloSide.SetDeadline(deadline)
	_ = peerSide.SetDeadline(deadline)

	if _, err := peerSide.Write(LegacyShareOpeningRecord()); err != nil {
		t.Fatal(err)
	}
	openEvent := make([]byte, 10)
	if _, err := io.ReadFull(iloSide, openEvent); err != nil {
		t.Fatal(err)
	}
	wantOpen := LegacySharePeerEventRecord(net.ParseIP("192.0.2.25"), true)
	if !bytes.Equal(openEvent, wantOpen) {
		t.Fatalf("open event=%x want=%x", openEvent, wantOpen)
	}

	video := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	broadcastDone := make(chan struct{})
	go func() {
		hub.Broadcast(video)
		close(broadcastDone)
	}()
	gotVideo := make([]byte, len(video))
	if _, err := io.ReadFull(peerSide, gotVideo); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotVideo, video) {
		t.Fatalf("video=%x want=%x", gotVideo, video)
	}
	<-broadcastDone

	input := []byte{1, 0, 0, 0, 4, 0, 0, 0, 0, 0}
	if _, err := peerSide.Write(input); err != nil {
		t.Fatal(err)
	}
	gotInput := make([]byte, len(input))
	if _, err := io.ReadFull(iloSide, gotInput); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotInput, input) {
		t.Fatalf("input=%x want=%x", gotInput, input)
	}

	if _, err := peerSide.Write(LegacyShareClosingRecord()); err != nil {
		t.Fatal(err)
	}
	closeEvent := make([]byte, 10)
	if _, err := io.ReadFull(iloSide, closeEvent); err != nil {
		t.Fatal(err)
	}
	wantClose := LegacySharePeerEventRecord(net.ParseIP("192.0.2.25"), false)
	if !bytes.Equal(closeEvent, wantClose) {
		t.Fatalf("close event=%x want=%x", closeEvent, wantClose)
	}
}

func TestLegacySharePeerEventRecords(t *testing.T) {
	ipv4 := LegacySharePeerEventRecord(net.ParseIP("192.0.2.1"), true)
	if len(ipv4) != 10 || ipv4[0] != 14 || !bytes.Equal(ipv4[2:6], []byte{192, 0, 2, 1}) {
		t.Fatalf("IPv4 opening=%x", ipv4)
	}
	ipv6 := LegacySharePeerEventRecord(net.ParseIP("2001:db8::1"), false)
	if len(ipv6) != 20 || ipv6[0] != 17 || ipv6[2] != 24 || !bytes.Equal(ipv6[4:], net.ParseIP("2001:db8::1").To16()) {
		t.Fatalf("IPv6 closing=%x", ipv6)
	}
}

func TestLegacyShareDecisionWords(t *testing.T) {
	underlying := &shortWriteConn{limit: 4}
	conn := &Conn{net: underlying}
	if err := conn.SendLegacyShareDecision(true); err != nil {
		t.Fatal(err)
	}
	if err := conn.SendLegacyShareDecision(false); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(underlying.written, []byte{4, 0, 0, 0, 3, 0, 0, 0}) {
		t.Fatalf("decision words=%x", underlying.written)
	}
}

func TestWriteAllHandlesShortWrites(t *testing.T) {
	underlying := &shortWriteConn{limit: 2}
	want := []byte{1, 2, 3, 4, 5}
	if err := writeAll(underlying, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(underlying.written, want) {
		t.Fatalf("written=%x want=%x", underlying.written, want)
	}
}
