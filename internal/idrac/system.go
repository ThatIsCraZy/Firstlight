package idrac

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// BootOverrideStatus mirrors the Redfish boot override fields.
type BootOverrideStatus struct {
	Target  string `json:"target"`
	Enabled string `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
}

// ManagementStatus is the power and boot state of the managed server.
type ManagementStatus struct {
	PowerState   string             `json:"power_state"`
	BootOverride BootOverrideStatus `json:"boot_override"`
}

// BootOverrideChange records what a one-time boot request did.
type BootOverrideChange struct {
	Device   string             `json:"device"`
	Before   BootOverrideStatus `json:"before"`
	Current  BootOverrideStatus `json:"current"`
	Verified bool               `json:"verified"`
}

type computerSystem struct {
	PowerState string `json:"PowerState"`
	Boot       struct {
		Target  string `json:"BootSourceOverrideTarget"`
		Enabled string `json:"BootSourceOverrideEnabled"`
		Mode    string `json:"BootSourceOverrideMode"`
	} `json:"Boot"`
	Status struct {
		Health string `json:"Health"`
		State  string `json:"State"`
	} `json:"Status"`
}

// GetManagementStatus reads power state and boot override over Redfish.
func (c *Client) GetManagementStatus(ctx context.Context) (ManagementStatus, error) {
	var system computerSystem
	if err := c.getJSON(ctx, systemPath, &system); err != nil {
		return ManagementStatus{}, err
	}
	return ManagementStatus{
		PowerState: strings.ToLower(system.PowerState),
		BootOverride: BootOverrideStatus{
			Target:  system.Boot.Target,
			Enabled: system.Boot.Enabled,
			Mode:    system.Boot.Mode,
		},
	}, nil
}

// SetOneTimeBoot arms a single boot from the virtual optical drive and reads
// the value back so the caller learns whether the firmware accepted it.
func (c *Client) SetOneTimeBoot(ctx context.Context, device string) (BootOverrideChange, error) {
	target, err := bootTarget(device)
	if err != nil {
		return BootOverrideChange{}, err
	}
	before, err := c.GetManagementStatus(ctx)
	if err != nil {
		return BootOverrideChange{}, err
	}
	change := BootOverrideChange{Device: strings.ToLower(strings.TrimSpace(device)), Before: before.BootOverride}
	payload := map[string]any{
		"Boot": map[string]any{
			"BootSourceOverrideTarget":  target,
			"BootSourceOverrideEnabled": "Once",
		},
	}
	if err := c.sendJSON(ctx, http.MethodPatch, systemPath, payload); err != nil {
		change.Current = before.BootOverride
		return change, err
	}
	after, err := c.GetManagementStatus(ctx)
	if err != nil {
		return change, err
	}
	change.Current = after.BootOverride
	change.Verified = strings.EqualFold(after.BootOverride.Target, target) &&
		strings.EqualFold(after.BootOverride.Enabled, "Once")
	if !change.Verified {
		return change, fmt.Errorf("iDRAC reported boot override %q/%q after the change",
			after.BootOverride.Target, after.BootOverride.Enabled)
	}
	return change, nil
}

func bootTarget(device string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(device)) {
	case "cd", "dvd", "cdrom", "virtual_cd":
		return "Cd", nil
	case "pxe":
		return "Pxe", nil
	case "hdd", "disk":
		return "Hdd", nil
	case "bios", "setup":
		return "BiosSetup", nil
	case "usb", "floppy":
		return "Floppy", nil
	default:
		return "", fmt.Errorf("unsupported one-time boot device %q", device)
	}
}

// ResetAction is a Redfish ComputerSystem.Reset value.
type ResetAction string

const (
	ResetOn                ResetAction = "On"
	ResetForceOff          ResetAction = "ForceOff"
	ResetGracefulShutdown  ResetAction = "GracefulShutdown"
	ResetForceRestart      ResetAction = "ForceRestart"
	ResetPowerCycle        ResetAction = "PowerCycle"
	ResetNmi               ResetAction = "Nmi"
	ResetGracefulRestart   ResetAction = "GracefulRestart"
	ResetPushPowerButton   ResetAction = "PushPowerButton"
	ResetForceOffThenOn    ResetAction = "ForceOffThenOn" // not a Redfish value, handled by the caller
	ResetActionUnsupported ResetAction = ""
)

// Reset performs a Redfish power action.
func (c *Client) Reset(ctx context.Context, action ResetAction) error {
	if action == "" {
		return errors.New("reset action is required")
	}
	return c.sendJSON(ctx, http.MethodPost, resetAction, map[string]any{
		"ResetType": string(action),
	})
}

type managerResponse struct {
	FirmwareVersion  string `json:"FirmwareVersion"`
	Model            string `json:"Model"`
	GraphicalConsole struct {
		ServiceEnabled        bool     `json:"ServiceEnabled"`
		MaxConcurrentSessions int      `json:"MaxConcurrentSessions"`
		ConnectTypesSupported []string `json:"ConnectTypesSupported"`
	} `json:"GraphicalConsole"`
}

// Info describes the controller and what it offers.
type Info struct {
	Model            string
	FirmwareVersion  string
	ConsoleEnabled   bool
	MaxConsoleUsers  int
	ServiceTag       string
	SystemModel      string
	ManagerMACAddres string
}

// Info reads the manager and system description.
func (c *Client) Info(ctx context.Context) (Info, error) {
	var manager managerResponse
	if err := c.getJSON(ctx, managerPath, &manager); err != nil {
		return Info{}, err
	}
	info := Info{
		Model:           manager.Model,
		FirmwareVersion: manager.FirmwareVersion,
		ConsoleEnabled:  manager.GraphicalConsole.ServiceEnabled,
		MaxConsoleUsers: manager.GraphicalConsole.MaxConcurrentSessions,
	}
	var root serviceRoot
	if err := c.getJSON(ctx, redfishRoot, &root); err == nil {
		info.ServiceTag = root.Oem.Dell.ServiceTag
		info.ManagerMACAddres = root.Oem.Dell.ManagerMACAddress
	}
	var system struct {
		Model string `json:"Model"`
	}
	if err := c.getJSON(ctx, systemPath, &system); err == nil {
		info.SystemModel = system.Model
	}
	return info, nil
}

type attributeResponse struct {
	Attributes map[string]any `json:"Attributes"`
}

// ConsoleSettings reports the parts of the iDRAC attribute set that decide
// whether a console session can start at all.
type ConsoleSettings struct {
	Enabled        bool
	ActiveSessions int
	MaxSessions    int
	// SharingDefault is what the firmware does when a sharing request is not
	// answered in time: "Deny Access", "Read Only" or "Full Access".
	SharingDefault string
	VirtualMedia   bool
	VNCEnabled     bool
}

// ConsoleSettings reads the virtual console attributes.
func (c *Client) ConsoleSettings(ctx context.Context) (ConsoleSettings, error) {
	var response attributeResponse
	if err := c.getJSON(ctx, attributes, &response); err != nil {
		return ConsoleSettings{}, err
	}
	settings := ConsoleSettings{
		Enabled:        attributeEquals(response.Attributes, "VirtualConsole.1.Enable", "Enabled"),
		ActiveSessions: attributeInt(response.Attributes, "VirtualConsole.1.ActiveSessions"),
		MaxSessions:    attributeInt(response.Attributes, "VirtualConsole.1.MaxSessions"),
		SharingDefault: attributeString(response.Attributes, "VirtualConsole.1.AccessPrivilege"),
		VirtualMedia:   attributeEquals(response.Attributes, "VirtualMedia.1.Enable", "Enabled"),
		VNCEnabled:     attributeEquals(response.Attributes, "VNCServer.1.Enable", "Enabled"),
	}
	return settings, nil
}

func attributeString(attrs map[string]any, key string) string {
	if value, ok := attrs[key].(string); ok {
		return value
	}
	return ""
}

func attributeEquals(attrs map[string]any, key, want string) bool {
	return strings.EqualFold(attributeString(attrs, key), want)
}

func attributeInt(attrs map[string]any, key string) int {
	switch value := attrs[key].(type) {
	case float64:
		return int(value)
	case string:
		var n int
		if _, err := fmt.Sscanf(value, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

// ScreenPreview fetches the still image the iDRAC keeps of the host screen.
// It needs no console session, which makes it a cheap health check.
func (c *Client) ScreenPreview(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(previewPath), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png")
	resp, body, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("console preview: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// VirtualMediaState is what the controller reports about its virtual drive.
type VirtualMediaState struct {
	Inserted     bool   `json:"inserted"`
	ImageName    string `json:"image_name,omitempty"`
	ConnectedVia string `json:"connected_via,omitempty"`
}

// VirtualMediaState reads the state of the virtual optical drive over Redfish.
// It is independent of the media channel, which makes it a useful check that
// an attached image really reached the firmware.
func (c *Client) VirtualMediaState(ctx context.Context) (VirtualMediaState, error) {
	var response struct {
		Inserted     bool   `json:"Inserted"`
		ImageName    string `json:"ImageName"`
		ConnectedVia string `json:"ConnectedVia"`
	}
	if err := c.getJSON(ctx, virtualMediaCDPath, &response); err != nil {
		return VirtualMediaState{}, err
	}
	return VirtualMediaState{
		Inserted:     response.Inserted,
		ImageName:    response.ImageName,
		ConnectedVia: response.ConnectedVia,
	}, nil
}

// SessionInfo describes one open session on the controller.
type SessionInfo struct {
	ID       string `json:"id"`
	UserName string `json:"user_name"`
	ClientIP string `json:"client_ip"`
	Type     string `json:"type"`
	Created  string `json:"created,omitempty"`
}

// Sessions lists the sessions the controller currently holds.
//
// A virtual console belongs to the web session that requested its ticket, so a
// console left behind by a client that died without logging out stays on the
// controller's books until its web session goes away. There are only six
// console slots, and the firmware ships with the console timeout disabled.
func (c *Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	var response struct {
		Members []struct {
			ID          string `json:"Id"`
			UserName    string `json:"UserName"`
			ClientIP    string `json:"ClientOriginIPAddress"`
			SessionType string `json:"SessionType"`
			CreatedTime string `json:"CreatedTime"`
		} `json:"Members"`
	}
	if err := c.getJSON(ctx, sessionsPath+"?$expand=*($levels=1)", &response); err != nil {
		return nil, err
	}
	sessions := make([]SessionInfo, 0, len(response.Members))
	for _, member := range response.Members {
		sessions = append(sessions, SessionInfo{
			ID:       member.ID,
			UserName: member.UserName,
			ClientIP: member.ClientIP,
			Type:     member.SessionType,
			Created:  member.CreatedTime,
		})
	}
	return sessions, nil
}

// CloseSession ends one session by id.
func (c *Client) CloseSession(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("session id is required")
	}
	return c.sendJSON(ctx, http.MethodDelete, sessionsPath+"/"+id, nil)
}

// CloseWebSessionsFor ends every web session belonging to the given user,
// except the one whose id is kept. It returns how many were closed.
//
// This is the Dell counterpart of seizing a busy iLO console, and like that it
// only ever runs because the operator asked for it: the sessions it ends may
// belong to a colleague who is working on the machine.
func (c *Client) CloseWebSessionsFor(ctx context.Context, user, keepID string) (int, error) {
	sessions, err := c.Sessions(ctx)
	if err != nil {
		return 0, err
	}
	closed := 0
	var failures []string
	for _, session := range sessions {
		if session.ID == keepID || !strings.EqualFold(session.Type, "WebUI") {
			continue
		}
		if user != "" && !strings.EqualFold(session.UserName, user) {
			continue
		}
		if err := c.CloseSession(ctx, session.ID); err != nil {
			failures = append(failures, session.ID)
			continue
		}
		closed++
	}
	if len(failures) > 0 {
		return closed, fmt.Errorf("could not close session(s) %s", strings.Join(failures, ", "))
	}
	return closed, nil
}

// newestWebSessionFor reports the highest-numbered web session of a user,
// which is the one a login just created.
func newestWebSessionFor(sessions []SessionInfo, user, clientIP string) string {
	best, bestValue := "", -1
	for _, session := range sessions {
		if !strings.EqualFold(session.Type, "WebUI") {
			continue
		}
		if user != "" && !strings.EqualFold(session.UserName, user) {
			continue
		}
		if clientIP != "" && session.ClientIP != clientIP {
			continue
		}
		value := 0
		if _, err := fmt.Sscanf(session.ID, "%d", &value); err != nil {
			continue
		}
		if value > bestValue {
			best, bestValue = session.ID, value
		}
	}
	return best
}
