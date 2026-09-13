package vmedia

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
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

func TestISOCloseDuringRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.iso")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x5a}, sectorSize*64), 0600); err != nil {
		t.Fatal(err)
	}
	iso, err := OpenISO(path)
	if err != nil {
		t.Fatal(err)
	}

	// The SCSI loop reads while another goroutine closes the session; a read
	// either returns data or reports the closed ISO, never a torn descriptor.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for n := 0; n < 200; n++ {
			buf, err := iso.ReadAt(0, sectorSize)
			if err != nil {
				continue
			}
			if len(buf) != sectorSize {
				t.Errorf("short read: %d bytes", len(buf))
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		if err := iso.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	wg.Wait()

	if _, err := iso.ReadAt(0, sectorSize); err == nil {
		t.Fatal("read after close succeeded")
	}
	if err := iso.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
