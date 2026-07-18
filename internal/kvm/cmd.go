package kvm

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxCommandDataSize = 1500

type CommandPacket struct {
	Command uint32
	Size    uint32
	Seq     uint16
	Flags   uint16
	Data    []byte
}

func (c *Conn) ReadCommandPacket() (CommandPacket, error) {
	return readCommandPacket(c)
}

func readCommandPacket(r io.Reader) (CommandPacket, error) {
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return CommandPacket{}, err
	}
	p := CommandPacket{
		Command: binary.LittleEndian.Uint32(header[0:4]),
		Size:    binary.LittleEndian.Uint32(header[4:8]),
		Seq:     binary.LittleEndian.Uint16(header[8:10]),
		Flags:   binary.LittleEndian.Uint16(header[10:12]),
	}
	if p.Size > maxCommandDataSize {
		return p, fmt.Errorf("command packet size %d exceeds %d", p.Size, maxCommandDataSize)
	}
	if p.Size == 0 {
		return p, nil
	}
	p.Data = make([]byte, p.Size)
	_, err := io.ReadFull(r, p.Data)
	return p, err
}
