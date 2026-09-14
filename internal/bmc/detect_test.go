package bmc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSplitAddress(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port uint16
		fail bool
	}{
		{in: "192.0.2.10", host: "192.0.2.10", port: 443},
		{in: "https://ilo.example/", host: "ilo.example", port: 443},
		{in: "idrac.example:8443", host: "idrac.example", port: 8443},
		{in: "[2001:db8::1]:4443", host: "2001:db8::1", port: 4443},
		{in: "  ", fail: true},
		{in: "host:0", fail: true},
	}
	for _, tc := range cases {
		host, port, err := splitAddress(tc.in)
		if tc.fail {
			if err == nil {
				t.Fatalf("expected an error for %q", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("splitAddress(%q): %v", tc.in, err)
		}
		if host != tc.host || port != tc.port {
			t.Fatalf("splitAddress(%q) = %q:%d, want %q:%d", tc.in, host, port, tc.host, tc.port)
		}
	}
}

// detectAgainst runs detection against a test server that answers the two
// documents a controller serves without authentication.
func detectAgainst(t *testing.T, redfish, rimp string) (Details, error) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/redfish/v1" && redfish != "":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(redfish))
		case strings.HasPrefix(r.URL.Path, "/xmldata") && rimp != "":
			w.Header().Set("Content-Type", "text/xml")
			_, _ = w.Write([]byte(rimp))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Detect(ctx, Options{Addr: strings.TrimPrefix(server.URL, "https://"), Timeout: 4 * time.Second})
}

func TestDetectRecognisesDell(t *testing.T) {
	const root = `{
		"Vendor": "Dell",
		"Product": "Integrated Dell Remote Access Controller",
		"Oem": {"Dell": {"ServiceTag": "ABC1234"}}
	}`
	details, err := detectAgainst(t, root, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != VendorDell {
		t.Fatalf("vendor = %q", details.Vendor)
	}
	if details.Identifier != "ABC1234" {
		t.Fatalf("identifier = %q", details.Identifier)
	}
}

func TestDetectRecognisesHPE(t *testing.T) {
	const root = `{
		"Vendor": "HPE",
		"Product": "Integrated Lights-Out",
		"Oem": {"Hpe": {"SerialNumber": "ILOABC", "Manager": [{"FirmwareVersion": "2.78"}]}}
	}`
	details, err := detectAgainst(t, root, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != VendorHPE {
		t.Fatalf("vendor = %q", details.Vendor)
	}
	if details.Firmware != "2.78" {
		t.Fatalf("firmware = %q", details.Firmware)
	}
}

// An older iLO 4 answers Redfish without naming a vendor. The RIMP document,
// which only HPE serves, has to settle it.
func TestDetectFallsBackToRIMP(t *testing.T) {
	const rimp = `<RIMP><HSI><SBSN>CZ123</SBSN><ProductName>ProLiant DL380 Gen9</ProductName></HSI>` +
		`<MP><PN>Integrated Lights-Out 4</PN><FWRI>2.82</FWRI></MP></RIMP>`
	details, err := detectAgainst(t, `{"Product": "unspecified"}`, rimp)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != VendorHPE {
		t.Fatalf("vendor = %q", details.Vendor)
	}
	if details.Model != "ProLiant DL380 Gen9" {
		t.Fatalf("model = %q", details.Model)
	}
	if details.Identifier != "CZ123" {
		t.Fatalf("identifier = %q", details.Identifier)
	}
}

// A Dell controller answers /xmldata with 404, so a stray RIMP probe must not
// turn a Dell into an HPE.
func TestDetectDellIsNotOverriddenByAMissingRIMP(t *testing.T) {
	details, err := detectAgainst(t, `{"Vendor": "Dell"}`, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != VendorDell {
		t.Fatalf("vendor = %q", details.Vendor)
	}
}

func TestDetectReportsUnknownVendor(t *testing.T) {
	details, err := detectAgainst(t, `{"Product": "Some BMC"}`, "")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != VendorUnknown {
		t.Fatalf("vendor = %q, want unknown", details.Vendor)
	}
}

func TestDetectFailsWhenNothingAnswers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Port 1 has no listener on any sane machine.
	if _, err := Detect(ctx, Options{Addr: "127.0.0.1:1", Timeout: 2 * time.Second}); err == nil {
		t.Fatal("expected an error when nothing answers")
	}
}

func TestParseRIMPRejectsOtherDocuments(t *testing.T) {
	if _, err := parseRIMP([]byte(`<html><body>not a RIMP</body></html>`)); err == nil {
		t.Fatal("expected an error for a document that is not a RIMP response")
	}
}

func TestVendorString(t *testing.T) {
	if VendorDell.String() != "Dell iDRAC" || VendorHPE.String() != "HPE iLO" {
		t.Fatal("vendor names are shown to users and must stay readable")
	}
	if VendorUnknown.String() != "unknown" {
		t.Fatalf("unknown vendor renders as %q", VendorUnknown.String())
	}
}
