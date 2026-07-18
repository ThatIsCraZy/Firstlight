package ilo

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type RCInfo struct {
	Enabled          bool
	MasterKey        []byte
	ProtocolVersion  int
	RCPort           uint16
	VMPort           uint16
	VMKey            string
	OptionalFeatures map[string]bool
	Source           string
}

func (c *Client) GetRCInfo(ctx context.Context) (*RCInfo, error) {
	var redfish redfishRCInfo
	err := c.getJSON(ctx, "/redfish/v1/Managers/1/RcInfo/", &redfish)
	if err == nil {
		return redfish.normalize()
	}
	var httpErr HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		return nil, err
	}

	var legacy legacyRCInfo
	if err := c.getJSON(ctx, "/json/rc_info", &legacy); err != nil {
		return nil, err
	}
	return legacy.normalize()
}

type redfishRCInfo struct {
	Enabled         bool   `json:"Enabled"`
	MasterKey       string `json:"MasterKey"`
	ProtocolVersion int    `json:"ProtocolVersion"`
	RCPort          uint16 `json:"RcPort"`
	VMPort          uint16 `json:"VmPort"`
}

func (r redfishRCInfo) normalize() (*RCInfo, error) {
	key, err := hex.DecodeString(strings.TrimSpace(r.MasterKey))
	if err != nil {
		return nil, fmt.Errorf("bad Redfish MasterKey: %w", err)
	}
	return &RCInfo{
		Enabled:         r.Enabled,
		MasterKey:       key,
		ProtocolVersion: r.ProtocolVersion,
		RCPort:          r.RCPort,
		VMPort:          r.VMPort,
		Source:          "redfish",
	}, nil
}

type legacyRCInfo struct {
	HTTPSPort        string      `json:"https_port"`
	EncKey           string      `json:"enc_key"`
	EncType          int         `json:"enc_type"`
	RCPort           int         `json:"rc_port"`
	VMKey            string      `json:"vm_key"`
	VMPort           int         `json:"vm_port"`
	ProtocolVersion  json.Number `json:"protocol_version"`
	OptionalFeatures string      `json:"optional_features"`
}

func (r legacyRCInfo) normalize() (*RCInfo, error) {
	key, err := hex.DecodeString(strings.TrimSpace(r.EncKey))
	if err != nil {
		return nil, fmt.Errorf("bad legacy enc_key: %w", err)
	}
	pv, err := parseProtocolVersion(r.ProtocolVersion)
	if err != nil {
		return nil, err
	}
	if r.RCPort < 1 || r.RCPort > 65535 {
		return nil, fmt.Errorf("bad legacy rc_port %d", r.RCPort)
	}
	if r.VMPort < 0 || r.VMPort > 65535 {
		return nil, fmt.Errorf("bad legacy vm_port %d", r.VMPort)
	}
	return &RCInfo{
		Enabled:          true,
		MasterKey:        key,
		ProtocolVersion:  pv,
		RCPort:           uint16(r.RCPort),
		VMPort:           uint16(r.VMPort),
		VMKey:            r.VMKey,
		OptionalFeatures: parseFeatures(r.OptionalFeatures),
		Source:           "legacy",
	}, nil
}

func parseProtocolVersion(n json.Number) (int, error) {
	if n == "" {
		return 1, nil
	}
	if i, err := strconv.Atoi(n.String()); err == nil {
		return i, nil
	}
	f, err := strconv.ParseFloat(n.String(), 32)
	if err != nil {
		return 0, fmt.Errorf("bad protocol_version %q: %w", n.String(), err)
	}
	return int(f), nil
}

func parseFeatures(s string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part != "" {
			out[part] = true
		}
	}
	return out
}
