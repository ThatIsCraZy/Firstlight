package kvm

import (
	"encoding/binary"
	"fmt"
)

type Channel uint8
type Command uint8
type Status uint8

const (
	ChannelKVM           Channel = 0
	ChannelCmd           Channel = 1
	ChannelFloppy        Channel = 2
	ChannelDisc          Channel = 3
	ChannelUSBKey        Channel = 4
	ChannelVirtualFolder Channel = 5

	CommandNew     Command = 0
	CommandAcquire Command = 1
	CommandShare   Command = 2

	StatusSuccess      Status = 0
	StatusDenied       Status = 1
	StatusBusy         Status = 2
	StatusNotSupported Status = 3
	StatusBadRequest   Status = 4
	StatusInternal     Status = 5
	StatusNoSessions   Status = 6
	StatusNotLicensed  Status = 7
)

const (
	clientHelloLen = 16 + 1 + 1 + 4 + 32
	serverHelloLen = 16 + 1 + 4 + 32
)

type ClientHello struct {
	IV         [16]byte
	Command    Command
	Channel    Channel
	Flags      uint32
	SessionKey [32]byte
}

type ServerHello struct {
	IV         [16]byte
	Status     Status
	Flags      uint32
	SessionKey [32]byte
}

func NewClientHello(iv []byte, command Command, channel Channel, sessionKey string) ClientHello {
	var h ClientHello
	copy(h.IV[:], iv)
	h.Command = command
	h.Channel = channel
	copy(h.SessionKey[:], []byte(sessionKey))
	return h
}

func (h ClientHello) MarshalBinary() []byte {
	out := make([]byte, clientHelloLen)
	copy(out[0:16], h.IV[:])
	out[16] = byte(h.Command)
	out[17] = byte(h.Channel)
	binary.LittleEndian.PutUint32(out[18:22], h.Flags)
	copy(out[22:54], h.SessionKey[:])
	return out
}

func UnmarshalServerHello(b []byte) (ServerHello, error) {
	var h ServerHello
	if len(b) != serverHelloLen {
		return h, fmt.Errorf("server hello must be %d bytes, got %d", serverHelloLen, len(b))
	}
	copy(h.IV[:], b[0:16])
	h.Status = Status(b[16])
	h.Flags = binary.LittleEndian.Uint32(b[17:21])
	copy(h.SessionKey[:], b[21:53])
	return h, nil
}

func (s Status) Error() string {
	switch s {
	case StatusSuccess:
		return "success"
	case StatusDenied:
		return "connection denied"
	case StatusBusy:
		return "remote console busy"
	case StatusNotSupported:
		return "not supported"
	case StatusBadRequest:
		return "bad request"
	case StatusInternal:
		return "internal iLO error"
	case StatusNoSessions:
		return "no free iLO sessions"
	case StatusNotLicensed:
		return "remote console not licensed"
	default:
		return fmt.Sprintf("iLO status %d", s)
	}
}
