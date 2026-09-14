package bmc

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// parseRIMP reads the HPE RIMP document. It is split out so the parser can be
// tested without a network.
func parseRIMP(body []byte) (Details, error) {
	var document rimp
	if err := xml.Unmarshal(body, &document); err != nil {
		return Details{}, fmt.Errorf("RIMP document is not XML: %w", err)
	}
	product := strings.TrimSpace(document.MP.Product)
	if product == "" && strings.TrimSpace(document.HSI.SBSN) == "" {
		return Details{}, fmt.Errorf("document at /xmldata is not a RIMP response")
	}
	details := Details{
		Vendor:     VendorHPE,
		Product:    product,
		Model:      strings.TrimSpace(document.HSI.Model),
		Identifier: strings.TrimSpace(document.HSI.SBSN),
		Firmware:   strings.TrimSpace(document.MP.Firmware),
	}
	if details.Product == "" {
		details.Product = "Integrated Lights-Out"
	}
	return details, nil
}
