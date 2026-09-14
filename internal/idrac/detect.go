package idrac

const redfishRoot = "/redfish/v1"

// serviceRoot is the unauthenticated Redfish document every BMC serves. Dell
// fills in Vendor and an Oem.Dell block, which is what identifies the
// controller before any credentials exist.
type serviceRoot struct {
	Vendor         string `json:"Vendor"`
	Product        string `json:"Product"`
	RedfishVersion string `json:"RedfishVersion"`
	Oem            struct {
		Dell struct {
			ServiceTag        string `json:"ServiceTag"`
			ManagerMACAddress string `json:"ManagerMACAddress"`
		} `json:"Dell"`
	} `json:"Oem"`
}
