package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"firstlight/internal/vmedia"
)

type fakeVirtualMedia struct {
	done   chan struct{}
	closed bool
}

type healthyFakeVirtualMedia struct {
	*fakeVirtualMedia
	health vmedia.Health
}

func (f *healthyFakeVirtualMedia) Health() vmedia.Health { return f.health }

func (f *fakeVirtualMedia) Close() error {
	f.closed = true
	return nil
}

func (f *fakeVirtualMedia) Done() <-chan struct{} { return f.done }

func TestMountISOIsDisabledWithoutConfiguredRoot(t *testing.T) {
	path := `Z:\secret\private.iso`
	session := &Session{}
	status, err := session.MountISO(context.Background(), path)
	if err == nil || status.Enabled || status.Mounted {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if strings.Contains(err.Error(), path) {
		t.Fatalf("mount error leaks ISO path: %q", err)
	}
}

func TestUnmountISOIsIdempotent(t *testing.T) {
	media := &fakeVirtualMedia{done: make(chan struct{})}
	session := &Session{
		isoRoot:          &ISORoot{},
		virtualMedia:     media,
		virtualMediaPath: `C:\iso\installer.iso`,
		virtualMediaName: "installer.iso",
		virtualMediaSize: 4096,
	}
	status, err := session.UnmountISO()
	if err != nil {
		t.Fatal(err)
	}
	if !media.closed || status.Mounted || status.ISOName != "" {
		t.Fatalf("unexpected status after unmount: %+v", status)
	}
	status, err = session.UnmountISO()
	if err != nil || status.Mounted {
		t.Fatalf("repeated unmount status=%+v err=%v", status, err)
	}
}

func TestVirtualMediaStatusDistinguishesTransportAndDeviceReadiness(t *testing.T) {
	media := &healthyFakeVirtualMedia{
		fakeVirtualMedia: &fakeVirtualMedia{done: make(chan struct{})},
		health: vmedia.Health{
			TransportAlive: true,
			DeviceReady:    true,
			ReadBytes:      8192,
			DeliveredBytes: 4096,
		},
	}
	session := &Session{
		isoRoot:          &ISORoot{},
		virtualMedia:     media,
		virtualMediaName: "installer.iso",
		virtualMediaSize: 16384,
	}
	status := session.VirtualMediaStatus()
	if !status.Mounted || !status.TransportAlive || !status.DeviceReady ||
		status.ReadBytes != 8192 || status.DeliveredBytes != 4096 ||
		status.ISOName != "installer.iso" {
		t.Fatalf("status=%+v", status)
	}
}

func TestConsoleCloseUnmountsVirtualMedia(t *testing.T) {
	media := &fakeVirtualMedia{done: make(chan struct{})}
	session := &Session{
		cancel:       func() {},
		notify:       make(chan struct{}),
		done:         make(chan struct{}),
		virtualMedia: media,
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !media.closed {
		t.Fatal("console close did not unmount virtual media")
	}
}

func TestTTLReapingUnmountsVirtualMedia(t *testing.T) {
	manager := NewManager(context.Background(), ManagerOptions{SessionTTL: 100 * time.Millisecond})
	defer manager.Close()
	media := &fakeVirtualMedia{done: make(chan struct{})}
	session := &Session{
		cancel:       func() {},
		notify:       make(chan struct{}),
		done:         make(chan struct{}),
		virtualMedia: media,
	}
	manager.mu.Lock()
	manager.sessions["test"] = &managedSession{session: session, lastUsed: time.Now().Add(-time.Second)}
	manager.mu.Unlock()
	deadline := time.After(2 * time.Second)
	for !media.closed {
		select {
		case <-deadline:
			t.Fatal("TTL reaping did not unmount virtual media")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
