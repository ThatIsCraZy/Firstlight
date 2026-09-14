package idrac

import "testing"

func TestParseAddress(t *testing.T) {
	cases := []struct {
		name string
		in   string
		host string
		port uint16
		fail bool
	}{
		{name: "bare address", in: "192.0.2.10", host: "192.0.2.10", port: 443},
		{name: "with scheme", in: "https://192.0.2.10", host: "192.0.2.10", port: 443},
		{name: "with trailing slash", in: "https://idrac.example/", host: "idrac.example", port: 443},
		{name: "explicit port", in: "idrac.example:8443", host: "idrac.example", port: 8443},
		{name: "ipv6 literal", in: "[2001:db8::1]", host: "2001:db8::1", port: 443},
		{name: "ipv6 with port", in: "[2001:db8::1]:4443", host: "2001:db8::1", port: 4443},
		{name: "empty", in: "   ", fail: true},
		{name: "bad port", in: "idrac.example:0", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, port, err := ParseAddress(tc.in)
			if tc.fail {
				if err == nil {
					t.Fatalf("expected an error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAddress(%q): %v", tc.in, err)
			}
			if host != tc.host || port != tc.port {
				t.Fatalf("= %q:%d, want %q:%d", host, port, tc.host, tc.port)
			}
		})
	}
}

func TestParseTicket(t *testing.T) {
	location := "https://192.0.2.10:443/restgui/vconsole/index.html?ip=192.0.2.10&kvmport=443" +
		"&title=idrac-ABC1234,%20PowerEdge%20R750,%20User:%20root" +
		"&VCSID=170106174133250104190193&VCSID2=082150107206015038244051"
	ticket, err := parseTicket(location)
	if err != nil {
		t.Fatalf("parseTicket: %v", err)
	}
	if ticket.Key1 != "170106174133250104190193" {
		t.Fatalf("key1 = %q", ticket.Key1)
	}
	if ticket.Key2 != "082150107206015038244051" {
		t.Fatalf("key2 = %q", ticket.Key2)
	}
	if ticket.Port != 443 {
		t.Fatalf("port = %d", ticket.Port)
	}
	if ticket.Title == "" {
		t.Fatal("title should be carried through")
	}
}

func TestParseTicketUsesKVMPort(t *testing.T) {
	ticket, err := parseTicket("https://host/x?kvmport=5900&VCSID=abc&VCSID2=def")
	if err != nil {
		t.Fatalf("parseTicket: %v", err)
	}
	if ticket.Port != 5900 {
		t.Fatalf("port = %d, want 5900", ticket.Port)
	}
}

func TestParseTicketRejectsEmptyLocation(t *testing.T) {
	if _, err := parseTicket(""); err == nil {
		t.Fatal("expected an error when the firmware returns no location")
	}
}

func TestParseTicketRejectsMissingKey(t *testing.T) {
	if _, err := parseTicket("https://host/x?kvmport=443"); err == nil {
		t.Fatal("expected an error when the ticket carries no session key")
	}
}

func TestBootTarget(t *testing.T) {
	cases := map[string]string{
		"cd":   "Cd",
		"CD":   "Cd",
		"dvd":  "Cd",
		"pxe":  "Pxe",
		"hdd":  "Hdd",
		"bios": "BiosSetup",
		"usb":  "Floppy",
	}
	for in, want := range cases {
		got, err := bootTarget(in)
		if err != nil {
			t.Fatalf("bootTarget(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("bootTarget(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := bootTarget("tape"); err == nil {
		t.Fatal("expected an error for an unknown device")
	}
}

func TestPowerMappingCoversEveryAction(t *testing.T) {
	cases := []struct {
		action    PowerAction
		operation byte
		reset     ResetAction
	}{
		{PowerMomentaryPress, PowerGracefulShutdown, ResetGracefulShutdown},
		{PowerPressAndHold, PowerOff, ResetForceOff},
		{PowerColdBootAction, PowerColdBoot, ResetPowerCycle},
		{PowerResetAction, PowerWarmBoot, ResetForceRestart},
	}
	for _, tc := range cases {
		operation, reset, err := powerMapping(tc.action)
		if err != nil {
			t.Fatalf("powerMapping(%q): %v", tc.action, err)
		}
		if operation != tc.operation || reset != tc.reset {
			t.Fatalf("powerMapping(%q) = %d/%q, want %d/%q",
				tc.action, operation, reset, tc.operation, tc.reset)
		}
	}
	if _, _, err := powerMapping("explode"); err == nil {
		t.Fatal("expected an error for an unknown action")
	}
}

func TestRFBButtonsReordersHIDMask(t *testing.T) {
	cases := []struct {
		hid  byte
		want byte
	}{
		{0, 0},
		{hidButtonLeft, rfbButtonLeft},
		{hidButtonRight, rfbButtonRight},
		{hidButtonMiddle, rfbButtonMiddle},
		{hidButtonLeft | hidButtonRight, rfbButtonLeft | rfbButtonRight},
	}
	for _, tc := range cases {
		if got := rfbButtons(tc.hid); got != tc.want {
			t.Fatalf("rfbButtons(%#b) = %#b, want %#b", tc.hid, got, tc.want)
		}
	}
}

func TestAttributeHelpers(t *testing.T) {
	attrs := map[string]any{
		"VirtualConsole.1.Enable":         "Enabled",
		"VirtualConsole.1.ActiveSessions": float64(2),
		"VirtualConsole.1.MaxSessions":    "6",
	}
	if !attributeEquals(attrs, "VirtualConsole.1.Enable", "enabled") {
		t.Fatal("attributeEquals should ignore case")
	}
	if got := attributeInt(attrs, "VirtualConsole.1.ActiveSessions"); got != 2 {
		t.Fatalf("active sessions = %d", got)
	}
	if got := attributeInt(attrs, "VirtualConsole.1.MaxSessions"); got != 6 {
		t.Fatalf("max sessions = %d", got)
	}
	if got := attributeInt(attrs, "missing"); got != 0 {
		t.Fatalf("missing attribute = %d, want 0", got)
	}
}

// A query string must survive into the query component; putting it in the path
// makes the firmware answer with the unfiltered document or a 404.
func TestClientURLKeepsQueryStringSeparate(t *testing.T) {
	client, err := NewClient(Options{Addr: "192.0.2.10"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	got := client.url("/redfish/v1/SessionService/Sessions?$expand=*($levels=1)")
	want := "https://192.0.2.10:443/redfish/v1/SessionService/Sessions?$expand=*($levels=1)"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if plain := client.url("/redfish/v1"); plain != "https://192.0.2.10:443/redfish/v1" {
		t.Fatalf("url without query = %q", plain)
	}
}
