package ilo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const computerSystemPath = "/redfish/v1/Systems/1/"

var (
	ErrManagementUnsupported = errors.New("Redfish system management is unsupported by this iLO firmware")
	ErrSystemPOSTBusy        = errors.New("one-time boot override is unavailable while server POST is in progress")
)

type BootOverrideStatus struct {
	Target  string `json:"target"`
	Enabled string `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
}

type ManagementStatus struct {
	PowerState   string             `json:"power_state"`
	BootOverride BootOverrideStatus `json:"boot_override"`
}

type BootOverrideChange struct {
	Device   string             `json:"device"`
	Before   BootOverrideStatus `json:"before"`
	Current  BootOverrideStatus `json:"current"`
	Verified bool               `json:"verified"`
}

type computerSystem struct {
	PowerState string      `json:"PowerState"`
	Boot       redfishBoot `json:"Boot"`
}

type redfishBoot struct {
	Target                 string   `json:"BootSourceOverrideTarget"`
	Enabled                string   `json:"BootSourceOverrideEnabled"`
	Mode                   string   `json:"BootSourceOverrideMode"`
	TargetAllowableValues  []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
	EnabledAllowableValues []string `json:"BootSourceOverrideEnabled@Redfish.AllowableValues"`
}

func (c *Client) GetManagementStatus(ctx context.Context) (ManagementStatus, error) {
	system, err := c.getComputerSystem(ctx)
	if err != nil {
		return ManagementStatus{}, err
	}
	return system.managementStatus(), nil
}

func (c *Client) SetOneTimeBoot(ctx context.Context, device string) (BootOverrideChange, error) {
	if !strings.EqualFold(strings.TrimSpace(device), "cd") {
		return BootOverrideChange{}, errors.New("one-time boot device must be \"cd\"")
	}

	before, err := c.getComputerSystem(ctx)
	if err != nil {
		return BootOverrideChange{}, err
	}
	change := BootOverrideChange{
		Device: "cd",
		Before: before.bootOverrideStatus(),
	}
	if !containsValue(before.Boot.TargetAllowableValues, "Cd") ||
		!containsValue(before.Boot.EnabledAllowableValues, "Once") {
		return change, ErrManagementUnsupported
	}

	request := struct {
		Boot struct {
			Target  string `json:"BootSourceOverrideTarget"`
			Enabled string `json:"BootSourceOverrideEnabled"`
		} `json:"Boot"`
	}{}
	request.Boot.Target = "Cd"
	request.Boot.Enabled = "Once"
	if err := c.patchJSON(ctx, computerSystemPath, request, nil); err != nil {
		return change, classifyManagementError("set one-time boot override", err)
	}

	current, err := c.getComputerSystem(ctx)
	if err != nil {
		return change, fmt.Errorf("verify one-time boot override: %w", err)
	}
	change.Current = current.bootOverrideStatus()
	change.Verified = strings.EqualFold(change.Current.Target, "Cd") &&
		strings.EqualFold(change.Current.Enabled, "Once")
	if !change.Verified {
		return change, errors.New("one-time boot override could not be verified after the update")
	}
	return change, nil
}

func (c *Client) getComputerSystem(ctx context.Context) (computerSystem, error) {
	var system computerSystem
	if err := c.getJSON(ctx, computerSystemPath, &system); err != nil {
		return computerSystem{}, classifyManagementError("read system status", err)
	}
	return system, nil
}

func (s computerSystem) managementStatus() ManagementStatus {
	return ManagementStatus{
		PowerState:   s.PowerState,
		BootOverride: s.bootOverrideStatus(),
	}
}

func (s computerSystem) bootOverrideStatus() BootOverrideStatus {
	return BootOverrideStatus{
		Target:  s.Boot.Target,
		Enabled: s.Boot.Enabled,
		Mode:    s.Boot.Mode,
	}
}

func containsValue(allowed []string, wanted string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), wanted) {
			return true
		}
	}
	return false
}

func classifyManagementError(action string, err error) error {
	var httpErr HTTPError
	if !errors.As(err, &httpErr) {
		return fmt.Errorf("iLO management %s failed: %w", action, err)
	}
	if isPOSTBusyResponse(httpErr.StatusCode, httpErr.Body) {
		return ErrSystemPOSTBusy
	}
	if httpErr.StatusCode == http.StatusNotFound ||
		httpErr.StatusCode == http.StatusMethodNotAllowed ||
		httpErr.StatusCode == http.StatusNotImplemented ||
		isUnsupportedResponse(httpErr.Body) {
		return ErrManagementUnsupported
	}
	return fmt.Errorf("iLO management %s failed with HTTP %d", action, httpErr.StatusCode)
}

func isPOSTBusyResponse(status int, body string) bool {
	if status != http.StatusConflict && status != http.StatusServiceUnavailable && status != http.StatusBadRequest {
		return false
	}
	text := strings.ToLower(redfishErrorText(body))
	return strings.Contains(text, "post") &&
		(strings.Contains(text, "progress") || strings.Contains(text, "busy") || strings.Contains(text, "current"))
}

func isUnsupportedResponse(body string) bool {
	text := strings.ToLower(redfishErrorText(body))
	return strings.Contains(text, "notsupported") ||
		strings.Contains(text, "not supported") ||
		strings.Contains(text, "propertyunknown") ||
		strings.Contains(text, "property not writable") ||
		strings.Contains(text, "propertyvaluenotinlist")
}

func redfishErrorText(body string) string {
	var value any
	if json.Unmarshal([]byte(body), &value) != nil {
		return body
	}
	var parts []string
	collectStrings(value, &parts)
	return strings.Join(parts, " ")
}

func collectStrings(value any, parts *[]string) {
	switch value := value.(type) {
	case string:
		*parts = append(*parts, value)
	case []any:
		for _, item := range value {
			collectStrings(item, parts)
		}
	case map[string]any:
		for key, item := range value {
			*parts = append(*parts, key)
			collectStrings(item, parts)
		}
	}
}
