package idrac

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Dell multiplexes its own control traffic into the RFB stream under message
// type 247. The subtype numbers below were read from the console application
// the iDRAC serves at /restgui/vconsole and confirmed against firmware
// 7.10.30.00 on a PowerEdge R750.
const (
	VendorMessage = 247

	MsgHostPowerState    = 161
	MsgNewClientJoined   = 162
	MsgChatMessage       = 163
	MsgSessionIsShared   = 164
	MsgSharingRequest    = 165
	MsgSharingApproved   = 166
	MsgFirstBootDevice   = 167
	MsgLockdownState     = 168
	MsgGracefulShutdown  = 169
	MsgStatisticsData    = 170
	MsgFPSData           = 171
	MsgClientListAuthKey = 172
	MsgClientExit        = 173
	MsgFramebufferUpdate = 174
	MsgVFlashPartitions  = 175
	MsgVFlashBootSelect  = 177
	MsgKeyboardLEDStatus = 178
	MsgKey2Request       = 179
)

// Client subcommands, sent as {247, 0, code, ...}. The key2 reply is the one
// exception: it carries no padding byte.
const (
	subFirstBoot     = 0xF0
	subPowerRequest  = 0xF1
	subChatMessage   = 0xF3
	subSharingAnswer = 0xF5
	subHeartBeat     = 0xF6
	subUSBReset      = 0xF8
	subWindowClose   = 0xF9
	subPixelFormat   = 0xFA
	subVFlashBoot    = 0xFB
	subFocusOut      = 0xFC
	subKey2Reply     = 0xB3
)

// HeartBeatInterval is how often the browser client sends its keep-alive. A
// console session that stops sending it is left registered on the controller,
// and since the firmware ships with the console timeout disabled, those stale
// sessions accumulate until the six available slots are gone.
const HeartBeatInterval = 20 * time.Second

// Power operations Dell accepts over the console channel. The same actions are
// reachable over Redfish, but the console path works while a Redfish job is
// pending and mirrors what the browser client does.
const (
	PowerGracefulShutdown = 0
	PowerOff              = 1
	PowerWarmBoot         = 2
	PowerColdBoot         = 3
	PowerOn               = 4
)

// Privilege bits reported in the client list message.
const (
	PrivilegeLogin          = 1 << 0
	PrivilegeConfigure      = 1 << 1
	PrivilegeConfigureUsers = 1 << 2
	PrivilegeClearLogs      = 1 << 3
	PrivilegeExecuteCommand = 1 << 4
	PrivilegeConsole        = 1 << 5
	PrivilegeVirtualMedia   = 1 << 6
)

// Control is one decoded Dell control message.
type Control struct {
	Subtype byte

	// PowerOn is set for MsgHostPowerState.
	PowerOn bool
	// BootDevice is set for MsgFirstBootDevice.
	BootDevice byte
	// LEDs is set for MsgKeyboardLEDStatus.
	LEDs byte
	// FPS is set for MsgFPSData, in frames per second.
	FPS byte

	// The fields below are filled for MsgClientListAuthKey.
	Key2       string
	AuthKey    uint32
	ClientID   uint32
	Privileges uint32
	UserName   string
	Clients    []Participant
}

// Participant is one viewer of a shared console session.
type Participant struct {
	ID   uint32
	Name string
	IP   string
}

// IsControlFrame reports whether a message carries Dell control traffic rather
// than RFB protocol bytes.
func IsControlFrame(frame []byte) bool {
	if len(frame) < 2 || frame[0] != VendorMessage {
		return false
	}
	switch frame[1] {
	case MsgHostPowerState, MsgNewClientJoined, MsgChatMessage, MsgSessionIsShared,
		MsgSharingRequest, MsgSharingApproved, MsgFirstBootDevice, MsgLockdownState,
		MsgGracefulShutdown, MsgStatisticsData, MsgFPSData, MsgClientListAuthKey,
		MsgClientExit, MsgFramebufferUpdate, MsgVFlashPartitions, MsgVFlashBootSelect,
		MsgKeyboardLEDStatus, MsgKey2Request:
		return true
	}
	return false
}

// ParseControl decodes a control message. Subtypes whose payload this client
// does not need come back with only Subtype filled in.
func ParseControl(frame []byte) (Control, error) {
	if !IsControlFrame(frame) {
		return Control{}, fmt.Errorf("not a Dell control message: % x", head(frame, 4))
	}
	control := Control{Subtype: frame[1]}
	body := frame[2:]
	switch control.Subtype {
	case MsgHostPowerState:
		if len(body) < 1 {
			return control, nil
		}
		control.PowerOn = body[0] != 0
	case MsgFirstBootDevice:
		if len(body) >= 1 {
			control.BootDevice = body[0]
		}
	case MsgKeyboardLEDStatus:
		if len(body) >= 1 {
			control.LEDs = body[0]
		}
	case MsgFPSData:
		if len(body) >= 1 {
			control.FPS = body[0]
		}
	case MsgClientListAuthKey:
		return parseClientList(control, body)
	}
	return control, nil
}

// Layout of the client list message, taken from the console application:
// four bytes auth key, 32 bytes key2, four bytes client id, four bytes
// privilege mask, 256 bytes user name, four bytes client count, the caller's
// own address, then one record per further viewer.
//
// The address field that follows the count is the one length the firmware does
// not agree with the console application about: firmware 7.10.30.00 sends 63
// bytes there while each viewer record carries 64. Rather than hard-code
// either number, the parser derives the address length from the message, which
// keeps it right whichever way a future firmware goes.
const (
	clientListHead  = 4 + 32 + 4 + 4 + clientNameLen + 4
	clientNameLen   = 256
	clientAddrLen   = 64
	clientRecordLen = 4 + clientNameLen + clientAddrLen
	maxParticipants = 64
)

func parseClientList(control Control, body []byte) (Control, error) {
	if len(body) < clientListHead {
		return control, fmt.Errorf("client list message is %d bytes, need at least %d",
			len(body), clientListHead)
	}
	control.AuthKey = binary.LittleEndian.Uint32(body[0:4])
	control.Key2 = trimNul(body[4:36])
	control.ClientID = binary.LittleEndian.Uint32(body[36:40])
	control.Privileges = binary.LittleEndian.Uint32(body[40:44])
	control.UserName = trimNul(body[44 : 44+clientNameLen])

	count := binary.LittleEndian.Uint32(body[clientListHead-4 : clientListHead])
	if count > maxParticipants {
		count = maxParticipants
	}
	tail := body[clientListHead:]
	// Whatever is not taken by the viewer records is the caller's own address.
	addressLen := len(tail) - int(count)*clientRecordLen
	if addressLen < 0 {
		// The count does not match the body: trust the body.
		count = uint32(len(tail) / clientRecordLen)
		addressLen = len(tail) - int(count)*clientRecordLen
	}
	offset := addressLen
	for i := uint32(0); i < count; i++ {
		if offset+clientRecordLen > len(tail) {
			break
		}
		record := tail[offset : offset+clientRecordLen]
		control.Clients = append(control.Clients, Participant{
			ID:   binary.LittleEndian.Uint32(record[0:4]),
			Name: trimNul(record[4 : 4+clientNameLen]),
			IP:   trimNul(record[4+clientNameLen:]),
		})
		offset += clientRecordLen
	}
	return control, nil
}

// Key2Reply builds the answer to MsgKey2Request. The trailing zero bytes are
// what the browser client sends and the firmware expects.
func Key2Reply(key2 string) []byte {
	out := make([]byte, 0, 2+len(key2)+8)
	out = append(out, VendorMessage, subKey2Reply)
	out = append(out, key2...)
	return append(out, make([]byte, 8)...)
}

// PowerRequest builds a console power command.
func PowerRequest(operation byte) []byte {
	return []byte{VendorMessage, 0, subPowerRequest, operation}
}

// FirstBootRequest selects the one-time boot device over the console channel.
func FirstBootRequest(device byte) []byte {
	return []byte{VendorMessage, 0, subFirstBoot, device}
}

// HeartBeat keeps the console session registered.
func HeartBeat() []byte {
	return []byte{VendorMessage, 0, subHeartBeat}
}

// WindowClose tells the firmware the viewer is going away. Without it the
// session stays on the controller's books.
func WindowClose() []byte {
	return []byte{VendorMessage, 0, subWindowClose}
}

// USBReset re-enumerates the virtual keyboard and mouse, which recovers a
// console whose input the host stopped accepting.
func USBReset() []byte {
	return []byte{VendorMessage, 0, subUSBReset}
}

// FocusOut tells the firmware the viewer lost keyboard focus, so it can drop
// any keys it still believes are held.
func FocusOut() []byte {
	return []byte{VendorMessage, 0, subFocusOut}
}

// SharingAnswer approves or denies a pending sharing request. Approval type 1
// grants access, 0 denies it.
func SharingAnswer(approve bool, clientID uint32) []byte {
	answer := byte(0)
	if approve {
		answer = 1
	}
	out := []byte{VendorMessage, 0, subSharingAnswer, answer, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(out[4:8], clientID)
	return out
}

// PixelFormatChange tells the firmware a SetPixelFormat message follows.
func PixelFormatChange() []byte {
	return []byte{VendorMessage, 0, subPixelFormat}
}

// SubtypeName renders a subtype for logs.
func SubtypeName(subtype byte) string {
	switch subtype {
	case MsgHostPowerState:
		return "HOST_POWER_STATE"
	case MsgNewClientJoined:
		return "NEW_CLIENT_JOINED"
	case MsgChatMessage:
		return "CHAT_MESSAGE"
	case MsgSessionIsShared:
		return "SESSION_IS_SHARED"
	case MsgSharingRequest:
		return "SHARING_REQUEST_RECEIVED"
	case MsgSharingApproved:
		return "SHARING_REQUEST_APPROVED"
	case MsgFirstBootDevice:
		return "FIRST_BOOT_DEVICE"
	case MsgLockdownState:
		return "SYSTEM_LOCKDOWN_STATE"
	case MsgGracefulShutdown:
		return "GRACEFULL_SHUTDOWN"
	case MsgStatisticsData:
		return "STATISTICS_DATA"
	case MsgFPSData:
		return "FPS_DATA"
	case MsgClientListAuthKey:
		return "CLIENT_LIST_AUTH_KEY"
	case MsgClientExit:
		return "CLIENT_EXIT"
	case MsgFramebufferUpdate:
		return "FRAME_BUFFER_UPDATE"
	case MsgVFlashPartitions:
		return "VFLASH_PARTITION_LIST"
	case MsgVFlashBootSelect:
		return "VFLASH_BOOT_SELECTION"
	case MsgKeyboardLEDStatus:
		return "KEYBOARD_LED_STATUS"
	case MsgKey2Request:
		return "KEY2_REQUEST_FROM_SERVER"
	}
	return fmt.Sprintf("subtype(%d)", subtype)
}

func trimNul(b []byte) string {
	if index := indexByte(b, 0); index >= 0 {
		b = b[:index]
	}
	return strings.TrimSpace(string(b))
}

func indexByte(b []byte, needle byte) int {
	for i, value := range b {
		if value == needle {
			return i
		}
	}
	return -1
}

func head(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
