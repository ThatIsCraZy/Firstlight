// Package bmc identifies which management controller answers at an address,
// so the user never has to pick a vendor by hand. Detection runs before any
// credentials are entered and relies only on documents a BMC serves to an
// unauthenticated client.
package bmc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Vendor names a controller family.
type Vendor string

const (
	// VendorUnknown means detection did not reach a conclusion.
	VendorUnknown Vendor = ""
	// VendorHPE is HPE Integrated Lights-Out.
	VendorHPE Vendor = "ilo"
	// VendorDell is Dell Integrated Dell Remote Access Controller.
	VendorDell Vendor = "idrac"
)

// String renders the vendor for user-facing text.
func (v Vendor) String() string {
	switch v {
	case VendorHPE:
		return "HPE iLO"
	case VendorDell:
		return "Dell iDRAC"
	}
	return "unknown"
}

// Details carries what detection learned. Everything beyond Vendor is
// best-effort and may be empty.
type Details struct {
	Vendor  Vendor
	Product string
	Model   string
	// Identifier is the service tag on Dell and the serial number on HPE.
	Identifier string
	// Firmware is the controller firmware version when the BMC publishes it
	// without authentication.
	Firmware string
}

// DefaultTimeout bounds one detection attempt.
const DefaultTimeout = 10 * time.Second

// Options configures Detect.
type Options struct {
	// Addr is a host name or address, optionally with a port.
	Addr string
	// VerifyCert turns on certificate verification.
	VerifyCert bool
	// Timeout bounds the whole detection. Zero uses DefaultTimeout.
	Timeout time.Duration
}

// Detect asks the address which controller it is. It sends no credentials.
func Detect(ctx context.Context, opts Options) (Details, error) {
	host, port, err := splitAddress(opts.Addr)
	if err != nil {
		return Details{}, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !opts.VerifyCert}, //nolint:gosec // BMCs ship self-signed certificates.
		},
	}
	base := "https://" + net.JoinHostPort(host, strconv.Itoa(int(port)))

	details, rootErr := detectViaRedfish(ctx, client, base)
	if rootErr == nil && details.Vendor != VendorUnknown {
		return details, nil
	}

	// Older iLO 4 firmware answers the Redfish root but omits the vendor, and
	// some controllers refuse it outright. The RIMP document is HPE-only and
	// settles those cases.
	if hpe, err := detectViaRIMP(ctx, client, base); err == nil {
		return hpe, nil
	}
	if rootErr != nil {
		return Details{}, rootErr
	}
	return details, nil
}

type serviceRoot struct {
	Vendor         string `json:"Vendor"`
	Product        string `json:"Product"`
	RedfishVersion string `json:"RedfishVersion"`
	Oem            map[string]struct {
		ServiceTag   string `json:"ServiceTag"`
		SerialNumber string `json:"SerialNumber"`
		Manager      []struct {
			FirmwareVersion string `json:"FirmwareVersion"`
		} `json:"Manager"`
	} `json:"Oem"`
}

func detectViaRedfish(ctx context.Context, client *http.Client, base string) (Details, error) {
	body, status, err := get(ctx, client, base+"/redfish/v1", "application/json")
	if err != nil {
		return Details{}, err
	}
	if status/100 != 2 {
		return Details{}, fmt.Errorf("Redfish service root returned HTTP %d", status)
	}
	var root serviceRoot
	if err := json.Unmarshal(body, &root); err != nil {
		return Details{}, fmt.Errorf("Redfish service root is not JSON: %w", err)
	}
	details := Details{Product: strings.TrimSpace(root.Product)}
	switch {
	case strings.EqualFold(root.Vendor, "Dell"):
		details.Vendor = VendorDell
	case strings.EqualFold(root.Vendor, "HPE"), strings.EqualFold(root.Vendor, "HP"):
		details.Vendor = VendorHPE
	}
	// The Oem block names the vendor even when the Vendor field is absent.
	for name, oem := range root.Oem {
		switch strings.ToLower(name) {
		case "dell":
			if details.Vendor == VendorUnknown {
				details.Vendor = VendorDell
			}
			if oem.ServiceTag != "" {
				details.Identifier = oem.ServiceTag
			}
		case "hpe", "hp":
			if details.Vendor == VendorUnknown {
				details.Vendor = VendorHPE
			}
			if oem.SerialNumber != "" {
				details.Identifier = oem.SerialNumber
			}
			if len(oem.Manager) > 0 {
				details.Firmware = oem.Manager[0].FirmwareVersion
			}
		}
	}
	if details.Vendor == VendorUnknown && strings.Contains(strings.ToLower(details.Product), "idrac") {
		details.Vendor = VendorDell
	}
	if details.Vendor == VendorUnknown && strings.Contains(strings.ToLower(details.Product), "integrated lights-out") {
		details.Vendor = VendorHPE
	}
	return details, nil
}

// rimp is the XML document only HPE iLO serves, at /xmldata?item=All. A Dell
// controller answers that path with 404, which makes it a clean tiebreaker.
type rimp struct {
	HSI struct {
		SPN   string `xml:"SPN"`
		SBSN  string `xml:"SBSN"`
		Model string `xml:"ProductName"`
	} `xml:"HSI"`
	MP struct {
		Product  string `xml:"PN"`
		Firmware string `xml:"FWRI"`
		Serial   string `xml:"SN"`
	} `xml:"MP"`
}

func detectViaRIMP(ctx context.Context, client *http.Client, base string) (Details, error) {
	body, status, err := get(ctx, client, base+"/xmldata?item=All", "text/xml")
	if err != nil {
		return Details{}, err
	}
	if status/100 != 2 {
		return Details{}, fmt.Errorf("RIMP document returned HTTP %d", status)
	}
	details, err := parseRIMP(body)
	if err != nil {
		return Details{}, err
	}
	return details, nil
}

func get(ctx context.Context, client *http.Client, target, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func splitAddress(addr string) (string, uint16, error) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return "", 0, errors.New("address is required")
	}
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "https://"), "http://")
	trimmed = strings.TrimSuffix(trimmed, "/")
	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return strings.Trim(trimmed, "[]"), 443, nil
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid port %q", portText)
	}
	return host, uint16(port), nil
}
