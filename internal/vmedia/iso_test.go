package vmedia

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestISOReadAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.iso")
	data := bytes.Repeat([]byte{0x5a}, sectorSize*2)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	iso, err := OpenISO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer iso.Close()
	got, err := iso.ReadAt(sectorSize, sectorSize)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data[sectorSize:]) {
		t.Fatal("read data mismatch")
	}
	if _, err := iso.ReadAt(int64(len(data)-1), 2); err == nil {
		t.Fatal("expected out-of-range error")
	}
}
