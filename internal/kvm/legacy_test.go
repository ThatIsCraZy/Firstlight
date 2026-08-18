package kvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestLegacyKVMNegotiationObfuscatesSessionKey(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverErr := make(chan error, 1)
	go func() {
		if _, err := server.Write([]byte{legacyMarker}); err != nil {
			serverErr <- err
			return
		}
		request := make([]byte, 34)
		if _, err := io.ReadFull(server, request); err != nil {
			serverErr <- err
			return
		}
		if got := binary.LittleEndian.Uint16(request[:2]); got != legacyKVMCommand|legacyEncryptedKey {
			serverErr <- &testError{"command", got, legacyKVMCommand | legacyEncryptedKey}
			return
		}
		wantKey := make([]byte, 32)
		copy(wantKey, "abc")
		mask := []byte("00112233445566778899aabbccddeeff")
		for i := 0; i < 3; i++ {
			wantKey[i] ^= mask[i]
		}
		if !bytes.Equal(request[2:], wantKey) {
			serverErr <- &testBytesError{"session key", request[2:], wantKey}
			return
		}
		_, err := server.Write([]byte{legacyResponseSuccess})
		serverErr <- err
	}()

	conn := &Conn{net: client}
	status, err := conn.negotiateLegacy(context.Background(), Info{
		ProtocolVersion: 1,
		SessionKey:      "abc",
		Command:         CommandNew,
		Channel:         ChannelKVM,
		Legacy: &LegacyOptions{
			EncryptionKey:     []byte("0123456789abcdef"),
			EncryptionKeyText: "00112233445566778899aabbccddeeff",
			EncryptSessionKey: true,
		},
	})
	if err != nil || status != StatusSuccess {
		t.Fatalf("negotiateLegacy()=(%v,%v)", status, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	if !conn.legacyKVM {
		t.Fatal("legacy KVM stream was not marked for negotiated encryption")
	}
}

func TestLegacyAcquireNegotiation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = server.Write([]byte{legacyMarker})
		request := make([]byte, 34)
		_, _ = io.ReadFull(server, request)
		_, _ = server.Write([]byte{legacyResponseBusy})
		var acquire [2]byte
		_, _ = io.ReadFull(server, acquire[:])
		if binary.LittleEndian.Uint16(acquire[:]) == legacyAcquireCommand {
			_, _ = server.Write([]byte{legacyResponseSuccess})
		}
	}()

	conn := &Conn{net: client}
	status, err := conn.negotiateLegacy(context.Background(), Info{
		ProtocolVersion: 1,
		SessionKey:      "session",
		Command:         CommandAcquire,
		Channel:         ChannelKVM,
		Legacy:          &LegacyOptions{EncryptionKey: []byte("0123456789abcdef")},
	})
	if err != nil || status != StatusSuccess {
		t.Fatalf("negotiateLegacy()=(%v,%v)", status, err)
	}
}

func TestLegacyShareNegotiationAcceptsReverseConnection(t *testing.T) {
	iloClient, iloServer := net.Pipe()
	defer iloServer.Close()
	peerClient, peerLeader := net.Pipe()
	defer peerLeader.Close()
	peerRecords := make(chan [legacyShareRecordLength]byte, 2)
	peerErr := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			var record [legacyShareRecordLength]byte
			if _, err := io.ReadFull(peerLeader, record[:]); err != nil {
				peerErr <- err
				return
			}
			peerRecords <- record
		}
	}()

	originalAccept := legacyShareAccept
	legacyShareAccept = func(context.Context, uint16, time.Duration) (net.Conn, error) {
		return peerClient, nil
	}
	defer func() { legacyShareAccept = originalAccept }()

	serverErr := make(chan error, 1)
	go func() {
		if _, err := iloServer.Write([]byte{legacyMarker}); err != nil {
			serverErr <- err
			return
		}
		var request [34]byte
		if _, err := io.ReadFull(iloServer, request[:]); err != nil {
			serverErr <- err
			return
		}
		if _, err := iloServer.Write([]byte{legacyResponseBusy}); err != nil {
			serverErr <- err
			return
		}
		var share [2]byte
		if _, err := io.ReadFull(iloServer, share[:]); err != nil {
			serverErr <- err
			return
		}
		if binary.LittleEndian.Uint16(share[:]) != legacyShareCommand {
			serverErr <- &testError{"share command", binary.LittleEndian.Uint16(share[:]), legacyShareCommand}
			return
		}
		_, err := iloServer.Write([]byte{legacyResponseSuccess})
		serverErr <- err
	}()

	conn := &Conn{net: iloClient}
	status, err := conn.negotiateLegacy(context.Background(), Info{
		ProtocolVersion: 1,
		Port:            17990,
		SessionKey:      "session",
		Command:         CommandShare,
		Channel:         ChannelKVM,
		Legacy:          &LegacyOptions{EncryptionKey: []byte("0123456789abcdef")},
	})
	if err != nil || status != StatusSuccess {
		t.Fatalf("negotiateLegacy()=(%v,%v)", status, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	opening := <-peerRecords
	if !bytes.Equal(opening[:], LegacyShareOpeningRecord()) {
		t.Fatalf("opening=%x", opening)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	closing := <-peerRecords
	if !bytes.Equal(closing[:], LegacyShareClosingRecord()) {
		t.Fatalf("closing=%x", closing)
	}
	select {
	case err := <-peerErr:
		t.Fatal(err)
	default:
	}
}

func TestAcceptLegacySharedPeerFromListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dialed := make(chan net.Conn, 1)
	go func() {
		conn, dialErr := net.Dial("tcp", listener.Addr().String())
		if dialErr == nil {
			dialed <- conn
		}
	}()
	accepted, err := acceptLegacySharedPeerFromListener(ctx, listener)
	if err != nil {
		t.Fatal(err)
	}
	_ = accepted.Close()
	_ = (<-dialed).Close()
}

func TestLegacyCommandNegotiationScansMarkerAndEncrypts(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	commandKey := []byte("fedcba9876543210")
	serverErr := make(chan error, 1)
	go func() {
		if _, err := server.Write([]byte{1, 2, 3, legacyMarker}); err != nil {
			serverErr <- err
			return
		}
		request := make([]byte, 34)
		if _, err := io.ReadFull(server, request); err != nil {
			serverErr <- err
			return
		}
		if got := binary.LittleEndian.Uint16(request[:2]); got != legacyCommandCommand {
			serverErr <- &testError{"command", got, legacyCommandCommand}
			return
		}
		if _, err := server.Write([]byte{legacyResponseSuccess}); err != nil {
			serverErr <- err
			return
		}
		var marker [4]byte
		if _, err := io.ReadFull(server, marker[:]); err != nil {
			serverErr <- err
			return
		}
		if marker != [4]byte{8, 0, 0, 0} {
			serverErr <- &testBytesError{"encryption marker", marker[:], []byte{8, 0, 0, 0}}
			return
		}
		wire := make([]byte, 4)
		if _, err := io.ReadFull(server, wire); err != nil {
			serverErr <- err
			return
		}
		stream, err := NewAESStream(commandKey, make([]byte, 16))
		if err != nil {
			serverErr <- err
			return
		}
		stream.XORKeyStream(wire, wire)
		if !bytes.Equal(wire, []byte{1, 2, 3, 4}) {
			serverErr <- &testBytesError{"command payload", wire, []byte{1, 2, 3, 4}}
			return
		}
		serverErr <- nil
	}()

	conn := &Conn{net: client}
	status, err := conn.negotiateLegacy(context.Background(), Info{
		ProtocolVersion: 1,
		SessionKey:      "session",
		Command:         CommandNew,
		Channel:         ChannelCmd,
		Legacy: &LegacyOptions{
			EncryptionKey:  []byte("0123456789abcdef"),
			CommandKey:     commandKey,
			EncryptCommand: true,
		},
	})
	if err != nil || status != StatusSuccess {
		t.Fatalf("negotiateLegacy()=(%v,%v)", status, err)
	}
	if _, err := conn.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy command server timed out")
	}
}

func TestLegacyCipherRoundTrips(t *testing.T) {
	key := []byte("0123456789abcdef")
	for _, mode := range []LegacyCipher{LegacyCipherRC4, LegacyCipherAES128, LegacyCipherAES256} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			tx, _, err := newLegacyStreamPair(mode, key)
			if err != nil {
				t.Fatal(err)
			}
			_, rx, err := newLegacyStreamPair(mode, key)
			if err != nil {
				t.Fatal(err)
			}
			plain := []byte("legacy iLO 4 KVM")
			wire := append([]byte(nil), plain...)
			tx.XORKeyStream(wire, wire)
			rx.XORKeyStream(wire, wire)
			if !bytes.Equal(wire, plain) {
				t.Fatalf("round trip=%x want=%x", wire, plain)
			}
		})
	}
}

type testError struct {
	field string
	got   uint16
	want  uint16
}

func (e *testError) Error() string {
	return fmt.Sprintf("%s=%#04x want=%#04x", e.field, e.got, e.want)
}

type testBytesError struct {
	field string
	got   []byte
	want  []byte
}

func (e *testBytesError) Error() string {
	return fmt.Sprintf("%s=%x want=%x", e.field, e.got, e.want)
}
