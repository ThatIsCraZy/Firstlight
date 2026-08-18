package console

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DefaultSessionTTL     = 15 * time.Minute
	DefaultConnectTimeout = 30 * time.Second
	MaximumObserveWait    = 30 * time.Second
	MaximumTextRunes      = 4096
)

var ErrUnknownHandle = errors.New("unknown or expired console handle")

type BusyMode string

const (
	BusyFail BusyMode = "fail"
)

type OpenOptions struct {
	Address            string
	Username           string
	Password           string
	InsecureSkipVerify bool
	BusyMode           BusyMode
}

func (o OpenOptions) validate() (OpenOptions, error) {
	o.Address = strings.TrimSpace(o.Address)
	o.Username = strings.TrimSpace(o.Username)
	o.BusyMode = BusyMode(strings.ToLower(strings.TrimSpace(string(o.BusyMode))))
	if o.Address == "" {
		return OpenOptions{}, errors.New("iLO address is required")
	}
	if o.Username == "" {
		return OpenOptions{}, errors.New("iLO username is required")
	}
	if o.Password == "" {
		return OpenOptions{}, errors.New("iLO password is required")
	}
	if o.BusyMode == "" {
		o.BusyMode = BusyFail
	}
	if o.BusyMode != BusyFail {
		return OpenOptions{}, fmt.Errorf("busy mode must be %q", BusyFail)
	}
	return o, nil
}

type State struct {
	Handle            string    `json:"handle"`
	Address           string    `json:"address"`
	Connected         bool      `json:"connected"`
	InputReady        bool      `json:"input_ready"`
	Shared            bool      `json:"shared"`
	ProtocolVersion   int       `json:"protocol_version"`
	Width             int       `json:"width"`
	Height            int       `json:"height"`
	Revision          uint64    `json:"revision"`
	FrameRevision     uint64    `json:"frame_revision"`
	ImageAvailable    bool      `json:"image_available"`
	Power             string    `json:"power"`
	POSTCode          string    `json:"post_code"`
	DisconnectReason  string    `json:"disconnect_reason,omitempty"`
	OpenedAt          time.Time `json:"opened_at"`
	LastFrameAt       time.Time `json:"last_frame_at,omitempty"`
	InsecureTransport bool      `json:"insecure_transport"`
}

type TextResult struct {
	Sent    int `json:"sent"`
	Skipped int `json:"skipped"`
}

type VirtualMediaStatus struct {
	Enabled        bool   `json:"enabled"`
	Mounted        bool   `json:"mounted"`
	TransportAlive bool   `json:"transport_alive"`
	DeviceReady    bool   `json:"device_ready"`
	ISOName        string `json:"iso_name,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	ReadBytes      uint64 `json:"read_bytes,omitempty"`
	DeliveredBytes uint64 `json:"delivered_bytes,omitempty"`
}

type BootOverrideStatus struct {
	Target  string `json:"target"`
	Enabled string `json:"enabled"`
	Mode    string `json:"mode,omitempty"`
}

type ManagementStatus struct {
	PowerState   string             `json:"power_state"`
	BootOverride BootOverrideStatus `json:"boot_override"`
}

type OneTimeBootResult struct {
	Device   string             `json:"device"`
	Before   BootOverrideStatus `json:"before"`
	Current  BootOverrideStatus `json:"current"`
	Verified bool               `json:"verified"`
}

type ManagerOptions struct {
	SessionTTL     time.Duration
	ConnectTimeout time.Duration
	ISORoot        *ISORoot
	Logger         func(format string, args ...any)
}
