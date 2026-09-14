package idrac

import (
	"encoding/binary"
	"testing"
)

func TestIsControlFrame(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
		want  bool
	}{
		{"key request", []byte{247, 179}, true},
		{"power state", []byte{247, 161, 1}, true},
		{"client list", []byte{247, 172, 0, 0}, true},
		{"unknown subtype", []byte{247, 5}, false},
		{"RFB framebuffer update", []byte{0, 0, 0, 1}, false},
		{"too short", []byte{247}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsControlFrame(tc.frame); got != tc.want {
				t.Fatalf("IsControlFrame(% x) = %v, want %v", tc.frame, got, tc.want)
			}
		})
	}
}

func TestParseControlPowerState(t *testing.T) {
	on, err := ParseControl([]byte{247, MsgHostPowerState, 1})
	if err != nil {
		t.Fatalf("ParseControl: %v", err)
	}
	if !on.PowerOn {
		t.Fatal("power state 1 should mean the host is on")
	}
	off, err := ParseControl([]byte{247, MsgHostPowerState, 0})
	if err != nil {
		t.Fatalf("ParseControl: %v", err)
	}
	if off.PowerOn {
		t.Fatal("power state 0 should mean the host is off")
	}
}

// clientListFrame builds the message the firmware sends right after the
// version exchange.
func clientListFrame(key2, user string, privileges uint32, others []Participant) []byte {
	// The firmware sends 63 bytes for the caller's own address.
	const ownAddrLen = 63
	body := make([]byte, clientListHead+ownAddrLen)
	binary.LittleEndian.PutUint32(body[0:4], 0xdeadbeef)
	copy(body[4:36], key2)
	binary.LittleEndian.PutUint32(body[36:40], 29)
	binary.LittleEndian.PutUint32(body[40:44], privileges)
	copy(body[44:44+clientNameLen], user)
	binary.LittleEndian.PutUint32(body[clientListHead-4:clientListHead], uint32(len(others)))
	copy(body[clientListHead:], "10.0.0.5")
	for _, participant := range others {
		record := make([]byte, clientRecordLen)
		binary.LittleEndian.PutUint32(record[0:4], participant.ID)
		copy(record[4:4+clientNameLen], participant.Name)
		copy(record[4+clientNameLen:], participant.IP)
		body = append(body, record...)
	}
	return append([]byte{247, MsgClientListAuthKey}, body...)
}

func TestParseControlClientList(t *testing.T) {
	frame := clientListFrame("013080081001193189181251", "root", 111, []Participant{
		{ID: 7, Name: "operator", IP: "10.0.0.9"},
	})
	control, err := ParseControl(frame)
	if err != nil {
		t.Fatalf("ParseControl: %v", err)
	}
	if control.UserName != "root" {
		t.Fatalf("user name = %q", control.UserName)
	}
	if control.Key2 != "013080081001193189181251" {
		t.Fatalf("key2 = %q", control.Key2)
	}
	if control.ClientID != 29 {
		t.Fatalf("client id = %d", control.ClientID)
	}
	if control.Privileges != 111 {
		t.Fatalf("privileges = %d", control.Privileges)
	}
	if len(control.Clients) != 1 {
		t.Fatalf("clients = %+v, want one", control.Clients)
	}
	if control.Clients[0].Name != "operator" || control.Clients[0].IP != "10.0.0.9" {
		t.Fatalf("client = %+v", control.Clients[0])
	}
}

func TestParseControlRejectsShortClientList(t *testing.T) {
	if _, err := ParseControl([]byte{247, MsgClientListAuthKey, 1, 2, 3}); err == nil {
		t.Fatal("expected an error for a truncated client list")
	}
}

// A firmware that claims a huge client count must not make the parser allocate
// without bound.
func TestParseControlCapsClientCount(t *testing.T) {
	frame := clientListFrame("k", "root", 1, nil)
	binary.LittleEndian.PutUint32(frame[2+clientListHead-4:], 1<<20)
	control, err := ParseControl(frame)
	if err != nil {
		t.Fatalf("ParseControl: %v", err)
	}
	if len(control.Clients) != 0 {
		t.Fatalf("clients = %d, want none since the body carries no records", len(control.Clients))
	}
}

func TestKey2ReplyLayout(t *testing.T) {
	key := "123456789012345678901234"
	reply := Key2Reply(key)
	if len(reply) != 34 {
		t.Fatalf("length = %d, want 34", len(reply))
	}
	if reply[0] != VendorMessage || reply[1] != subKey2Reply {
		t.Fatalf("header = % x", reply[:2])
	}
	if string(reply[2:26]) != key {
		t.Fatalf("key = %q", reply[2:26])
	}
	for _, b := range reply[26:] {
		if b != 0 {
			t.Fatalf("trailer = % x, want zeros", reply[26:])
		}
	}
}

func TestSharingAnswerLayout(t *testing.T) {
	approve := SharingAnswer(true, 0x01020304)
	if approve[0] != VendorMessage || approve[1] != 0 || approve[2] != subSharingAnswer {
		t.Fatalf("header = % x", approve[:3])
	}
	if approve[3] != 1 {
		t.Fatalf("approval byte = %d, want 1", approve[3])
	}
	if got := binary.LittleEndian.Uint32(approve[4:8]); got != 0x01020304 {
		t.Fatalf("client id = %#x", got)
	}
	deny := SharingAnswer(false, 1)
	if deny[3] != 0 {
		t.Fatalf("denial byte = %d, want 0", deny[3])
	}
}

func TestPowerRequestLayout(t *testing.T) {
	request := PowerRequest(PowerColdBoot)
	want := []byte{247, 0, subPowerRequest, PowerColdBoot}
	if len(request) != len(want) {
		t.Fatalf("length = %d", len(request))
	}
	for i := range want {
		if request[i] != want[i] {
			t.Fatalf("request = % x, want % x", request, want)
		}
	}
}
