package kvm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type AESStream struct {
	iv  []byte
	cbc cipher.BlockMode
	in  []byte
	buf []byte
	idx int
}

func NewAESStream(key, iv []byte) (*AESStream, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, fmt.Errorf("AES key must be 16, 24, or 32 bytes, got %d", len(key))
	}
	if len(iv) == 0 {
		iv = make([]byte, aes.BlockSize)
		if _, err := io.ReadFull(rand.Reader, iv); err != nil {
			return nil, err
		}
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("AES IV must be 16 bytes, got %d", len(iv))
	}
	block, err := aes.NewCipher(append([]byte(nil), key...))
	if err != nil {
		return nil, err
	}
	ivCopy := append([]byte(nil), iv...)
	return &AESStream{
		iv:  append([]byte(nil), iv...),
		cbc: cipher.NewCBCEncrypter(block, ivCopy),
		in:  make([]byte, aes.BlockSize),
		buf: make([]byte, aes.BlockSize),
		idx: aes.BlockSize,
	}, nil
}

func (s *AESStream) IV() []byte {
	return append([]byte(nil), s.iv...)
}

func (s *AESStream) XORKeyStream(dst, src []byte) {
	if len(dst) < len(src) {
		panic("kvm.AESStream: output smaller than input")
	}
	for i := range src {
		if s.idx >= len(s.buf) {
			s.cbc.CryptBlocks(s.buf, s.in)
			s.idx = 0
		}
		dst[i] = src[i] ^ s.buf[s.idx]
		s.idx++
	}
}
