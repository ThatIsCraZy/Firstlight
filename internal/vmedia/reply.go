package vmedia

import "encoding/binary"

const (
	replyMagic0 = 0xde
	replyMagic1 = 0xc0
	replyMagic2 = 0xad
	replyMagic3 = 0x0b

	flagKeepalive = 2
)

type ReplyHeader struct {
	Flags    uint32
	Media    byte
	SenseKey byte
	ASC      byte
	ASCQ     byte
	Length   uint32
}

func (h ReplyHeader) Bytes() [16]byte {
	var out [16]byte
	out[0] = replyMagic0
	out[1] = replyMagic1
	out[2] = replyMagic2
	out[3] = replyMagic3
	binary.LittleEndian.PutUint32(out[4:8], h.Flags)
	out[8] = h.Media
	out[9] = h.SenseKey
	out[10] = h.ASC
	out[11] = h.ASCQ
	binary.LittleEndian.PutUint32(out[12:16], h.Length)
	return out
}

func (h ReplyHeader) WithSense(sense, asc, ascq byte, length int) ReplyHeader {
	h.SenseKey = sense
	h.ASC = asc
	h.ASCQ = ascq
	h.Length = uint32(length)
	return h
}

func KeepaliveHeader() ReplyHeader {
	return ReplyHeader{Flags: flagKeepalive}
}

func SyncHeader(cmd []byte) [16]byte {
	h := KeepaliveHeader().Bytes()
	copy(h[8:], cmd)
	return h
}
