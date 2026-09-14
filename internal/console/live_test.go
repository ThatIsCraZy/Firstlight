package console

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveDellSessionThroughManager exercises the vendor dispatch end to end:
// the manager opens a session by address alone, and the Dell backend has to be
// selected, drive the console and answer the same calls the MCP server makes.
//
// It stays skipped unless the environment names a controller:
//
//	FIRSTLIGHT_IDRAC_ADDR, FIRSTLIGHT_IDRAC_USER, FIRSTLIGHT_IDRAC_PASSWORD
func TestLiveDellSessionThroughManager(t *testing.T) {
	addr := os.Getenv("FIRSTLIGHT_IDRAC_ADDR")
	user := os.Getenv("FIRSTLIGHT_IDRAC_USER")
	password := os.Getenv("FIRSTLIGHT_IDRAC_PASSWORD")
	if addr == "" || user == "" || password == "" {
		t.Skip("set FIRSTLIGHT_IDRAC_ADDR, FIRSTLIGHT_IDRAC_USER and FIRSTLIGHT_IDRAC_PASSWORD to run this test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A small image in a temporary root lets the test drive the whole media
	// path, which on the Dell side streams outbound over the console port.
	isoDir := t.TempDir()
	isoPath := filepath.Join(isoDir, "test.iso")
	payload := make([]byte, 4*1024*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := os.WriteFile(isoPath, payload, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	isoRoot, err := NewISORoot(isoDir)
	if err != nil {
		t.Fatalf("NewISORoot: %v", err)
	}

	manager := NewManager(ctx, ManagerOptions{
		ISORoot: isoRoot,
		Logger:  func(format string, args ...any) { t.Logf(format, args...) },
	})
	defer func() { _ = manager.Close() }()

	handle, state, err2 := manager.Open(ctx, OpenOptions{
		Address:            addr,
		Username:           user,
		Password:           password,
		InsecureSkipVerify: true,
	})
	if err2 != nil {
		t.Fatalf("Open: %v", err2)
	}
	if !state.Connected {
		t.Fatalf("state = %+v", state)
	}
	if state.ProtocolVersion != rfbProtocolVersion {
		t.Fatalf("protocol version = %d, want %d for a Dell controller",
			state.ProtocolVersion, rfbProtocolVersion)
	}
	session, err := manager.Get(handle)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Observe blocks until a frame arrives and returns it as PNG, which is
	// exactly what the MCP tool does. A console another viewer holds stays
	// dark by design, so the test steps aside for that.
	observed, image, err := session.Observe(ctx, 0, 20*time.Second)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if observed.WaitingForApproval {
		t.Skip("the controller is holding the console for another session")
	}
	if !observed.ImageAvailable || len(image) == 0 {
		t.Fatalf("no image: state %+v, %d bytes", observed, len(image))
	}
	if len(image) < 8 || string(image[1:4]) != "PNG" {
		t.Fatalf("image is not a PNG: % x", image[:8])
	}
	t.Logf("console %dx%d, power %q, %d byte PNG", observed.Width, observed.Height, observed.Power, len(image))

	status, err := session.ManagementStatus(ctx)
	if err != nil {
		t.Fatalf("ManagementStatus: %v", err)
	}
	if status.PowerState == "" {
		t.Fatal("power state should not be empty")
	}
	t.Logf("management: %+v", status)

	if err := session.PressKeys(ctx, []string{"SHIFT"}, 50*time.Millisecond); err != nil {
		t.Fatalf("PressKeys: %v", err)
	}
	if err := session.Mouse(ctx, "move", observed.Width/2, observed.Height/2, "", 0); err != nil {
		t.Fatalf("Mouse: %v", err)
	}

	media, err := session.MountISO(ctx, "test.iso")
	if err != nil {
		t.Fatalf("MountISO: %v", err)
	}
	if !media.Mounted || media.ISOName != "test.iso" {
		t.Fatalf("media status = %+v", media)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !session.VirtualMediaStatus().DeviceReady {
		if time.Now().After(deadline) {
			t.Fatalf("firmware did not attach the image: %+v", session.VirtualMediaStatus())
		}
		time.Sleep(500 * time.Millisecond)
	}
	ready := session.VirtualMediaStatus()
	t.Logf("virtual media: %+v", ready)
	if ready.DeliveredBytes == 0 {
		t.Fatal("no image data was delivered")
	}
	if _, err := session.UnmountISO(); err != nil {
		t.Fatalf("UnmountISO: %v", err)
	}
	if got := session.VirtualMediaStatus(); got.Mounted {
		t.Fatalf("image should be detached: %+v", got)
	}

	closed, err := manager.CloseSession(handle)
	if err != nil || !closed {
		t.Fatalf("CloseSession = %v, %v", closed, err)
	}
}
