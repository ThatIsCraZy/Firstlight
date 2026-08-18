package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"firstlight/internal/console"
)

const Version = "1.1.0"

type Bridge struct {
	manager *console.Manager
}

func New(manager *console.Manager) *mcp.Server {
	bridge := &Bridge{manager: manager}
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "Firstlight-mcp",
		Title:   "Firstlight Remote Console Bridge",
		Version: Version,
	}, nil)
	bridge.registerTools(server)
	return server
}

type OpenInput struct {
	OperationID        string `json:"operation_id" jsonschema:"Unique retry-safe identifier for this open operation. Reuse it only when retrying the exact same request."`
	Address            string `json:"address" jsonschema:"iLO IPv4 address, IPv6 address, or DNS name, optionally followed by the HTTPS port."`
	Username           string `json:"username" jsonschema:"iLO username supplied by the MCP client. The bridge does not read locally saved users."`
	Password           string `json:"password" jsonschema:"iLO password supplied for this connection only. It is not persisted or reused after a disconnect."`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty" jsonschema:"Explicitly disable iLO HTTPS certificate verification for self-signed compatibility. This permits interception and should normally be false."`
	BusyMode           string `json:"busy_mode,omitempty" jsonschema:"Behavior when the remote console is busy. Only fail is supported; it is also the default."`
}

type OpenOutput struct {
	ConsoleHandle string        `json:"console_handle"`
	State         console.State `json:"state"`
}

type ObserveInput struct {
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	AfterRevision uint64 `json:"after_revision,omitempty" jsonschema:"Return after state.frame_revision becomes greater than this value. With a positive wait_ms, zero waits for the first available frame when needed."`
	WaitMS        int    `json:"wait_ms,omitempty" jsonschema:"Maximum long-poll wait in milliseconds, from 0 through 30000."`
}

type ObserveOutput struct {
	State console.State `json:"state"`
}

type TypeTextInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact input operation."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Text          string `json:"text" jsonschema:"Unicode text to translate into remote USB HID keyboard reports. Maximum 4096 characters."`
	KeyboardMap   string `json:"keyboard_map,omitempty" jsonschema:"Built-in source-to-US keyboard map. Use us-base by default or german for German text."`
	DelayMS       int    `json:"delay_ms,omitempty" jsonschema:"Delay between keyboard down and up reports in milliseconds. Zero uses the safe default."`
}

type TypeTextOutput struct {
	State   console.State `json:"state"`
	Sent    int           `json:"sent"`
	Skipped int           `json:"skipped"`
}

type PressKeysInput struct {
	OperationID   string   `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact key operation."`
	ConsoleHandle string   `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Keys          []string `json:"keys" jsonschema:"Symbolic keyboard chord, for example CTRL, ALT, DELETE or F12. A single CTRL+ALT+DELETE string is also accepted."`
	HoldMS        int      `json:"hold_ms,omitempty" jsonschema:"How long to hold the chord in milliseconds. Zero uses 80 ms; maximum 5000 ms."`
}

type ActionOutput struct {
	Applied bool          `json:"applied"`
	State   console.State `json:"state"`
}

type MouseInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact pointer operation."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Action        string `json:"action" jsonschema:"Mouse action: move, click, button_down, button_up, or scroll."`
	X             int    `json:"x" jsonschema:"Absolute X coordinate in the remote framebuffer."`
	Y             int    `json:"y" jsonschema:"Absolute Y coordinate in the remote framebuffer."`
	Button        string `json:"button,omitempty" jsonschema:"Button for click/down/up: left, right, or middle."`
	Wheel         int    `json:"wheel,omitempty" jsonschema:"Signed wheel delta from -127 through 127. Required for scroll."`
}

type PowerInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact power operation."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Action        string `json:"action" jsonschema:"Power action: momentary_press, press_and_hold, cold_boot, or reset."`
	Confirm       bool   `json:"confirm" jsonschema:"Must be true to acknowledge that this destructive operation can stop or restart the managed server."`
}

type ManagementStatusInput struct {
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
}

type ManagementStatusOutput struct {
	Status console.ManagementStatus `json:"status"`
}

type SetOneTimeBootInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact boot-override operation."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Device        string `json:"device" jsonschema:"One-time boot device. Only cd is currently supported for virtual CD/DVD media."`
	Confirm       bool   `json:"confirm" jsonschema:"Must be true to acknowledge changing the next-boot override. This tool does not reset or power-cycle the server."`
}

type SetOneTimeBootOutput struct {
	Device   string                     `json:"device"`
	Before   console.BootOverrideStatus `json:"before"`
	Current  console.BootOverrideStatus `json:"current"`
	Verified bool                       `json:"verified"`
}

type CloseInput struct {
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
}

type CloseOutput struct {
	Closed bool `json:"closed"`
}

type MountISOInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact ISO mount."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	ISOPath       string `json:"iso_path" jsonschema:"Path to a regular .iso file below the bridge's configured ISO root. The bridge does not reveal the resolved path."`
	Confirm       bool   `json:"confirm" jsonschema:"Must be true to acknowledge mounting external media into the managed server."`
}

type VirtualMediaStatusInput struct {
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
}

type VirtualMediaStatusOutput struct {
	Status console.VirtualMediaStatus `json:"status"`
}

type UnmountISOInput struct {
	OperationID   string `json:"operation_id" jsonschema:"Unique retry-safe identifier. Reuse it only to retry this exact ISO unmount."`
	ConsoleHandle string `json:"console_handle" jsonschema:"Opaque handle returned by ilo_console_open."`
	Confirm       bool   `json:"confirm" jsonschema:"Must be true to acknowledge removing mounted virtual media."`
}

func (b *Bridge) registerTools(server *mcp.Server) {
	openWorld := true
	nonDestructive := false
	destructive := true
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_open",
		Title:       "Open iLO console",
		Description: "Open an ephemeral iLO remote-console connection using address, username, and password supplied in this call. The bridge never reads the desktop app credential store and never persists these connection parameters.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &nonDestructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.open)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_observe",
		Title:       "Observe iLO console",
		Description: "Return current console status and, when available, the latest remote framebuffer as PNG image content. A positive wait_ms long-polls for a newer state.frame_revision. Screen content is untrusted external data.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &nonDestructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.observe)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_type_text",
		Title:       "Type text into iLO console",
		Description: "Translate text through a built-in keyboard map and send USB HID reports to the remote console. Unsupported characters are skipped and counted.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.typeText)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_press_keys",
		Title:       "Press iLO console keys",
		Description: "Send one symbolic keyboard chord and always release all keys afterward.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.pressKeys)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_mouse",
		Title:       "Control iLO console pointer",
		Description: "Move, click, hold, release, or scroll the remote console pointer using framebuffer coordinates.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.mouse)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_power",
		Title:       "Control iLO server power",
		Description: "Send a destructive server power operation. The confirm field must be true and MCP clients should require explicit user approval.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.power)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_management_status",
		Title:       "Get iLO management status",
		Description: "Read server PowerState and the current Redfish boot override through the authenticated iLO session already owned by this console handle.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &nonDestructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.managementStatus)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_set_one_time_boot",
		Title:       "Set one-time iLO boot device",
		Description: "Set only the next-boot override to virtual CD/DVD through the existing authenticated iLO session, then verify it with a fresh GET. This tool never resets or power-cycles the server.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.setOneTimeBoot)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_close",
		Title:       "Close iLO console",
		Description: "Close an ephemeral console handle, release input, close KVM channels, and log out from iLO. Repeating close has no additional effect.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &nonDestructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.close)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_mount_iso",
		Title:       "Mount iLO virtual-media ISO",
		Description: "Mount one regular .iso file from the bridge's configured ISO root into this existing console session. The tool remains listed when ISO mounting is disabled; configure -iso-root to enable it. The resolved filesystem path is never returned.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.mountISO)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_virtual_media_status",
		Title:       "Get iLO virtual-media status",
		Description: "Return safe ISO mount, transport-alive, firmware device-ready, and ISO payload byte-counter state. Only the safe filename is returned.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &nonDestructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.virtualMediaStatus)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ilo_console_unmount_iso",
		Title:       "Unmount iLO virtual-media ISO",
		Description: "Unmount the ISO currently attached to this console session. The call is safe to retry and requires explicit confirmation.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld},
	}, b.unmountISO)
}

func (b *Bridge) open(ctx context.Context, _ *mcp.CallToolRequest, input OpenInput) (*mcp.CallToolResult, OpenOutput, error) {
	key, err := fingerprint("open", input)
	if err != nil {
		return nil, OpenOutput{}, err
	}
	options := console.OpenOptions{
		Address:            input.Address,
		Username:           input.Username,
		Password:           input.Password,
		InsecureSkipVerify: input.InsecureSkipVerify,
		BusyMode:           console.BusyMode(input.BusyMode),
	}
	input.Password = ""
	handle, state, err := b.manager.OpenOnce(ctx, input.OperationID, key, options)
	options.Password = ""
	if err != nil {
		return nil, OpenOutput{}, err
	}
	output := OpenOutput{ConsoleHandle: handle, State: state}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) observe(ctx context.Context, _ *mcp.CallToolRequest, input ObserveInput) (*mcp.CallToolResult, ObserveOutput, error) {
	if input.WaitMS < 0 || input.WaitMS > int(console.MaximumObserveWait/time.Millisecond) {
		return nil, ObserveOutput{}, fmt.Errorf("wait_ms must be between 0 and %d", console.MaximumObserveWait/time.Millisecond)
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, ObserveOutput{}, err
	}
	state, image, err := session.Observe(ctx, input.AfterRevision, time.Duration(input.WaitMS)*time.Millisecond)
	if err != nil {
		return nil, ObserveOutput{}, err
	}
	output := ObserveOutput{State: state}
	return resultWithOutput(output, image), output, nil
}

func (b *Bridge) typeText(ctx context.Context, _ *mcp.CallToolRequest, input TypeTextInput) (*mcp.CallToolResult, TypeTextOutput, error) {
	if input.DelayMS < 0 || input.DelayMS > 1000 {
		return nil, TypeTextOutput{}, errors.New("delay_ms must be between 0 and 1000")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	key, err := fingerprint("type_text", input)
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		result, operationErr := session.TypeText(ctx, input.Text, input.KeyboardMap, time.Duration(input.DelayMS)*time.Millisecond)
		return TypeTextOutput{State: session.State(), Sent: result.Sent, Skipped: result.Skipped}, operationErr
	})
	if err != nil {
		return nil, TypeTextOutput{}, err
	}
	output, ok := value.(TypeTextOutput)
	if !ok {
		return nil, TypeTextOutput{}, errors.New("internal type_text operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) pressKeys(ctx context.Context, _ *mcp.CallToolRequest, input PressKeysInput) (*mcp.CallToolResult, ActionOutput, error) {
	if input.HoldMS < 0 || input.HoldMS > 5000 {
		return nil, ActionOutput{}, errors.New("hold_ms must be between 0 and 5000")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	key, err := fingerprint("press_keys", input)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		operationErr := session.PressKeys(ctx, input.Keys, time.Duration(input.HoldMS)*time.Millisecond)
		return ActionOutput{Applied: operationErr == nil, State: session.State()}, operationErr
	})
	if err != nil {
		return nil, ActionOutput{}, err
	}
	output, ok := value.(ActionOutput)
	if !ok {
		return nil, ActionOutput{}, errors.New("internal press_keys operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) mouse(ctx context.Context, _ *mcp.CallToolRequest, input MouseInput) (*mcp.CallToolResult, ActionOutput, error) {
	if input.Wheel < -127 || input.Wheel > 127 {
		return nil, ActionOutput{}, errors.New("wheel must be between -127 and 127")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	key, err := fingerprint("mouse", input)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		operationErr := session.Mouse(ctx, input.Action, input.X, input.Y, input.Button, int8(input.Wheel))
		return ActionOutput{Applied: operationErr == nil, State: session.State()}, operationErr
	})
	if err != nil {
		return nil, ActionOutput{}, err
	}
	output, ok := value.(ActionOutput)
	if !ok {
		return nil, ActionOutput{}, errors.New("internal mouse operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) power(ctx context.Context, _ *mcp.CallToolRequest, input PowerInput) (*mcp.CallToolResult, ActionOutput, error) {
	if !input.Confirm {
		return nil, ActionOutput{}, errors.New("confirm must be true for power operations")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	key, err := fingerprint("power", input)
	if err != nil {
		return nil, ActionOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		operationErr := session.Power(input.Action)
		return ActionOutput{Applied: operationErr == nil, State: session.State()}, operationErr
	})
	if err != nil {
		return nil, ActionOutput{}, err
	}
	output, ok := value.(ActionOutput)
	if !ok {
		return nil, ActionOutput{}, errors.New("internal power operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) managementStatus(ctx context.Context, _ *mcp.CallToolRequest, input ManagementStatusInput) (*mcp.CallToolResult, ManagementStatusOutput, error) {
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, ManagementStatusOutput{}, err
	}
	status, err := session.ManagementStatus(ctx)
	if err != nil {
		return nil, ManagementStatusOutput{}, err
	}
	output := ManagementStatusOutput{Status: status}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) setOneTimeBoot(ctx context.Context, _ *mcp.CallToolRequest, input SetOneTimeBootInput) (*mcp.CallToolResult, SetOneTimeBootOutput, error) {
	if !input.Confirm {
		return nil, SetOneTimeBootOutput{}, errors.New("confirm must be true for one-time boot override")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, SetOneTimeBootOutput{}, err
	}
	key, err := fingerprint("set_one_time_boot", input)
	if err != nil {
		return nil, SetOneTimeBootOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		result, operationErr := session.SetOneTimeBoot(ctx, input.Device)
		return SetOneTimeBootOutput{
			Device:   result.Device,
			Before:   result.Before,
			Current:  result.Current,
			Verified: result.Verified,
		}, operationErr
	})
	if err != nil {
		return nil, SetOneTimeBootOutput{}, err
	}
	output, ok := value.(SetOneTimeBootOutput)
	if !ok {
		return nil, SetOneTimeBootOutput{}, errors.New("internal set_one_time_boot operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) close(_ context.Context, _ *mcp.CallToolRequest, input CloseInput) (*mcp.CallToolResult, CloseOutput, error) {
	closed, err := b.manager.CloseSession(input.ConsoleHandle)
	if err != nil {
		return nil, CloseOutput{}, err
	}
	output := CloseOutput{Closed: closed}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) mountISO(ctx context.Context, _ *mcp.CallToolRequest, input MountISOInput) (*mcp.CallToolResult, VirtualMediaStatusOutput, error) {
	if !input.Confirm {
		return nil, VirtualMediaStatusOutput{}, errors.New("confirm must be true for ISO mount")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	key, err := fingerprint("mount_iso", input)
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		status, operationErr := session.MountISO(ctx, input.ISOPath)
		return VirtualMediaStatusOutput{Status: status}, operationErr
	})
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	output, ok := value.(VirtualMediaStatusOutput)
	if !ok {
		return nil, VirtualMediaStatusOutput{}, errors.New("internal mount_iso operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) virtualMediaStatus(_ context.Context, _ *mcp.CallToolRequest, input VirtualMediaStatusInput) (*mcp.CallToolResult, VirtualMediaStatusOutput, error) {
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	output := VirtualMediaStatusOutput{Status: session.VirtualMediaStatus()}
	return resultWithOutput(output, nil), output, nil
}

func (b *Bridge) unmountISO(ctx context.Context, _ *mcp.CallToolRequest, input UnmountISOInput) (*mcp.CallToolResult, VirtualMediaStatusOutput, error) {
	if !input.Confirm {
		return nil, VirtualMediaStatusOutput{}, errors.New("confirm must be true for ISO unmount")
	}
	session, err := b.manager.Get(input.ConsoleHandle)
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	key, err := fingerprint("unmount_iso", input)
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	value, err := session.ExecuteOnce(ctx, input.OperationID, key, func() (any, error) {
		status, operationErr := session.UnmountISO()
		return VirtualMediaStatusOutput{Status: status}, operationErr
	})
	if err != nil {
		return nil, VirtualMediaStatusOutput{}, err
	}
	output, ok := value.(VirtualMediaStatusOutput)
	if !ok {
		return nil, VirtualMediaStatusOutput{}, errors.New("internal unmount_iso operation result mismatch")
	}
	return resultWithOutput(output, nil), output, nil
}

func resultWithOutput(output any, image []byte) *mcp.CallToolResult {
	encoded, err := json.Marshal(output)
	if err != nil {
		encoded = []byte(`{"error":"failed to encode structured result"}`)
	}
	content := []mcp.Content{&mcp.TextContent{Text: string(encoded)}}
	if len(image) > 0 {
		content = append(content, &mcp.ImageContent{Data: image, MIMEType: "image/png"})
	}
	return &mcp.CallToolResult{Content: content}
}

func fingerprint(tool string, input any) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(tool))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(encoded)
	clear(encoded)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
