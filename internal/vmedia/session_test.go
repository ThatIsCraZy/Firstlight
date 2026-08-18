package vmedia

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

type fakeConn struct {
	r      *bytes.Reader
	w      bytes.Buffer
	closed bool
}

func newFakeConn(cmds ...[]byte) *fakeConn {
	var in bytes.Buffer
	for _, cmd := range cmds {
		in.Write(cmd)
	}
	return &fakeConn{r: bytes.NewReader(in.Bytes())}
}

func (f *fakeConn) Read(p []byte) (int, error)  { return f.r.Read(p) }
func (f *fakeConn) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func TestCDImageInitialMediaChangedThenReady(t *testing.T) {
	iso := testISO(t, sectorSize*2)
	defer iso.Close()
	conn := newFakeConn(scsiCmd(0x00), scsiCmd(0x00))
	health := &healthCounters{}
	cd := newCDImage(conn, iso, nil, nil, health)
	if ok, err := cd.process(); err != nil || !ok {
		t.Fatalf("first process ok=%v err=%v", ok, err)
	}
	if ok, err := cd.process(); err != nil || !ok {
		t.Fatalf("second process ok=%v err=%v", ok, err)
	}
	out := conn.w.Bytes()
	if len(out) != 32 {
		t.Fatalf("reply len=%d", len(out))
	}
	if out[9] != 6 || out[10] != 40 {
		t.Fatalf("first sense=%x", out[:16])
	}
	if out[25] != 0 || out[26] != 0 {
		t.Fatalf("second sense=%x", out[16:32])
	}
	if !health.deviceReady.Load() {
		t.Fatal("successful TEST UNIT READY did not mark the device ready")
	}
}

func TestCDImageConstructorAlwaysProvidesWriteLock(t *testing.T) {
	cd := newCDImage(nil, nil, nil, nil)
	if cd.writeMu == nil {
		t.Fatal("write mutex was not initialized")
	}
}

func TestCDImageReadCapacityAndRead10(t *testing.T) {
	iso := testISO(t, sectorSize*2)
	defer iso.Close()
	read10 := scsiCmd(0x28)
	read10[5] = 1
	read10[8] = 1
	conn := newFakeConn(scsiCmd(0x25), read10)
	health := &healthCounters{}
	cd := newCDImage(conn, iso, nil, nil, health)
	cd.mediaState = 2
	cd.media = 1
	if ok, err := cd.process(); err != nil || !ok {
		t.Fatalf("capacity ok=%v err=%v", ok, err)
	}
	if ok, err := cd.process(); err != nil || !ok {
		t.Fatalf("read ok=%v err=%v", ok, err)
	}
	out := conn.w.Bytes()
	if len(out) != 16+8+16+sectorSize {
		t.Fatalf("reply len=%d", len(out))
	}
	if out[12] != 8 || out[16+6] != 8 {
		t.Fatalf("bad capacity reply: %x", out[:24])
	}
	readHeader := out[24:40]
	if readHeader[12] != 0 || readHeader[13] != 8 {
		t.Fatalf("bad read length header: %x", readHeader)
	}
	if got := health.readBytes.Load(); got != sectorSize {
		t.Fatalf("read bytes=%d want=%d", got, sectorSize)
	}
	if got := health.deliveredBytes.Load(); got != sectorSize {
		t.Fatalf("delivered bytes=%d want=%d", got, sectorSize)
	}
	if !health.deviceReady.Load() {
		t.Fatal("successful media read did not mark the device ready")
	}
}

func TestCDImageReadOutOfRange(t *testing.T) {
	iso := testISO(t, sectorSize)
	defer iso.Close()
	read10 := scsiCmd(0x28)
	read10[5] = 2
	read10[8] = 1
	conn := newFakeConn(read10)
	cd := newCDImage(conn, iso, nil, nil)
	cd.mediaState = 2
	cd.media = 1
	if ok, err := cd.process(); err != nil || !ok {
		t.Fatalf("process ok=%v err=%v", ok, err)
	}
	out := conn.w.Bytes()
	if len(out) != 16 || out[9] != 5 || out[10] != 33 {
		t.Fatalf("bad out-of-range reply: %x", out)
	}
}

func TestCDImageEjectStopsSession(t *testing.T) {
	iso := testISO(t, sectorSize)
	defer iso.Close()
	cmd := scsiCmd(0x1b)
	cmd[4] = 2
	conn := newFakeConn(cmd)
	cd := newCDImage(conn, iso, nil, nil)
	cd.mediaState = 2
	cd.media = 1
	ok, err := cd.process()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected eject to stop processing")
	}
}

func testISO(t *testing.T, size int) *ISO {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.iso")
	data := bytes.Repeat([]byte{0x42}, size)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	iso, err := OpenISO(path)
	if err != nil {
		t.Fatal(err)
	}
	return iso
}

func scsiCmd(op byte) []byte {
	cmd := make([]byte, 12)
	cmd[0] = op
	return cmd
}
