package kvm

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
)

type KDF struct {
	master  []byte
	result  []byte
	label   string
	context string
	count   uint32
	i       uint32
	offset  int
}

func NewKDF(master []byte) *KDF {
	m := append([]byte(nil), master...)
	return &KDF{
		master:  m,
		result:  make([]byte, sha512.Size),
		label:   "iLO IRC",
		context: "key derivation",
		count:   0x200,
	}
}

func (k *KDF) Derive(buf []byte) {
	remaining := len(buf)
	written := 0
	for remaining > 0 {
		if k.i == 0 || k.offset >= len(k.result) {
			k.generate()
		}
		n := min(remaining, len(k.result)-k.offset)
		copy(buf[written:written+n], k.result[k.offset:k.offset+n])
		written += n
		k.offset += n
		remaining -= n
	}
}

func (k *KDF) generate() {
	msg := k.message()
	h := hmac.New(sha512.New, k.master)
	_, _ = h.Write(msg)
	copy(k.result, h.Sum(nil))
	k.offset = 0
}

func (k *KDF) message() []byte {
	k.i++
	var prev []byte
	if k.i != 1 {
		prev = k.result
	}
	out := make([]byte, 0, len(prev)+len(k.label)+len(k.context)+9)
	out = append(out, prev...)
	out = binary.LittleEndian.AppendUint32(out, k.i)
	out = append(out, k.label...)
	out = append(out, 0)
	out = append(out, k.context...)
	out = binary.LittleEndian.AppendUint32(out, k.count)
	k.count += 0x200
	return out
}
