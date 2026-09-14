package idrac

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"firstlight/internal/bmc"
	"firstlight/internal/rfb"
	"firstlight/internal/vmedia"
)

// The tests in this file talk to a real controller. They stay skipped unless
// the three environment variables below are set, so an ordinary `go test`
// never needs hardware:
//
//	FIRSTLIGHT_IDRAC_ADDR, FIRSTLIGHT_IDRAC_USER, FIRSTLIGHT_IDRAC_PASSWORD
func liveConfig(t *testing.T) Config {
	t.Helper()
	addr := os.Getenv("FIRSTLIGHT_IDRAC_ADDR")
	user := os.Getenv("FIRSTLIGHT_IDRAC_USER")
	password := os.Getenv("FIRSTLIGHT_IDRAC_PASSWORD")
	if addr == "" || user == "" || password == "" {
		t.Skip("set FIRSTLIGHT_IDRAC_ADDR, FIRSTLIGHT_IDRAC_USER and FIRSTLIGHT_IDRAC_PASSWORD to run the live tests")
	}
	return Config{
		Addr:     addr,
		User:     user,
		Password: password,
		Logf:     func(format string, args ...any) { t.Logf(format, args...) },
	}
}

func TestLiveDetectReportsDell(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	details, err := bmc.Detect(ctx, bmc.Options{Addr: cfg.Addr})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if details.Vendor != bmc.VendorDell {
		t.Fatalf("vendor = %q, want %q", details.Vendor, bmc.VendorDell)
	}
	t.Logf("detected %s, product %q, service tag %q", details.Vendor, details.Product, details.Identifier)
}

func TestLiveClientReadsManagementStatus(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client, err := NewClient(Options{Addr: cfg.Addr})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Login(ctx, cfg.User, cfg.Password); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() { _ = client.Logout(context.Background()) }()

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	t.Logf("iDRAC %s firmware %s, system %s, service tag %s",
		info.Model, info.FirmwareVersion, info.SystemModel, info.ServiceTag)
	if !info.ConsoleEnabled {
		t.Fatal("virtual console is disabled on the test controller")
	}
	status, err := client.GetManagementStatus(ctx)
	if err != nil {
		t.Fatalf("GetManagementStatus: %v", err)
	}
	if status.PowerState == "" {
		t.Fatal("power state should not be empty")
	}
	t.Logf("power %s, boot override %+v", status.PowerState, status.BootOverride)

	settings, err := client.ConsoleSettings(ctx)
	if err != nil {
		t.Fatalf("ConsoleSettings: %v", err)
	}
	t.Logf("console settings %+v", settings)
}

func TestLiveConsoleDeliversAFrame(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	session, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	state := session.State()
	if !state.Connected || !state.InputReady {
		t.Fatalf("state after connect = %+v", state)
	}
	if state.Width <= 0 || state.Height <= 0 {
		t.Fatalf("console size = %dx%d", state.Width, state.Height)
	}
	t.Logf("console %dx%d", state.Width, state.Height)

	// Wait for the first painted frame. A console another viewer already holds
	// stays dark by design, which is an environment condition rather than a
	// defect, so the test steps aside for it.
	deadline := time.After(30 * time.Second)
	for {
		if session.Screen().Revision() > 0 {
			break
		}
		if session.State().WaitingForApproval {
			t.Skip("the controller is holding the console for another session")
		}
		select {
		case <-session.Changed():
		case <-session.Done():
			t.Fatalf("session ended early: %s", session.State().Disconnect)
		case <-deadline:
			t.Fatal("no framebuffer update arrived within 30 seconds")
		}
	}

	frame := session.Frame(nil)
	bounds := frame.Bounds()
	if bounds.Dx() != state.Width || bounds.Dy() != state.Height {
		t.Fatalf("frame is %v, state says %dx%d", bounds, state.Width, state.Height)
	}
	// A frame that is entirely one colour usually means the decoder ran but
	// painted nothing useful.
	first := frame.RGBAAt(bounds.Min.X, bounds.Min.Y)
	varied := false
	for y := bounds.Min.Y; y < bounds.Max.Y && !varied; y += 7 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 7 {
			if frame.RGBAAt(x, y) != first {
				varied = true
				break
			}
		}
	}
	if !varied {
		t.Log("warning: every sampled pixel is identical, the host may show a blank screen")
	}

	final := session.State()
	t.Logf("user %q, client id %d, privileges %#b, other viewers %d, revision %d",
		final.UserName, final.ClientID, final.Privileges, len(final.OtherUsers), final.Revision)
	if final.UserName == "" {
		t.Error("the firmware should have reported the session user name")
	}
}

// The keyboard path is exercised with a harmless key so a failure shows up
// here rather than in the user interface. Shift alone changes nothing on the
// host.
func TestLiveConsoleAcceptsInput(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	session, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	var shift [10]byte
	shift[0] = 1
	shift[2] = 0x02 // left shift
	if err := session.SendKeyboardReport(shift); err != nil {
		t.Fatalf("SendKeyboardReport: %v", err)
	}
	if err := session.SendAllKeysUp(); err != nil {
		t.Fatalf("SendAllKeysUp: %v", err)
	}
	state := session.State()
	if err := session.SendMouse(state.Width/2, state.Height/2, 0, 0, state.Width, state.Height, 0, 0); err != nil {
		t.Fatalf("SendMouse: %v", err)
	}
	if err := session.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := session.State(); !got.Connected {
		t.Fatalf("session dropped during input: %s", got.Disconnect)
	}
}

// TestLiveVirtualMediaMountsAnImage attaches an image and waits for the
// firmware to report it as mapped. Point FIRSTLIGHT_IDRAC_ISO at a real ISO to
// exercise a genuine image; without it the test builds a small blank one,
// which is enough to prove the transport.
func TestLiveVirtualMediaMountsAnImage(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	path := os.Getenv("FIRSTLIGHT_IDRAC_ISO")
	if path == "" {
		path = filepath.Join(t.TempDir(), "blank.iso")
		payload := make([]byte, 8*1024*1024)
		for i := range payload {
			payload[i] = byte(i % 251)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	iso, err := vmedia.OpenISO(path)
	if err != nil {
		t.Fatalf("OpenISO: %v", err)
	}
	defer func() { _ = iso.Close() }()

	session, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	if err := session.MountISO(ctx, iso, filepath.Base(path)); err != nil {
		t.Fatalf("MountISO: %v", err)
	}
	deadline := time.After(45 * time.Second)
	for {
		mounted, name, size, health := session.MediaStatus()
		if !mounted {
			t.Fatal("the media session ended before the firmware mapped it")
		}
		if health.DeviceReady {
			t.Logf("mapped %q, %d bytes, read %d, delivered %d",
				name, size, health.ReadBytes, health.DeliveredBytes)
			break
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-deadline:
			t.Fatalf("firmware did not map the image: health %+v", health)
		}
	}

	// Redfish reports the drive independently of the media channel, so it
	// confirms the image really reached the firmware.
	state, err := session.Client().VirtualMediaState(ctx)
	if err != nil {
		t.Fatalf("VirtualMediaState: %v", err)
	}
	if !state.Inserted {
		t.Fatalf("Redfish does not see the medium: %+v", state)
	}
	t.Logf("Redfish reports inserted=%v via %q, image %q",
		state.Inserted, state.ConnectedVia, state.ImageName)

	// The controller keeps reading while the host inspects the medium.
	time.Sleep(3 * time.Second)
	_, _, _, health := session.MediaStatus()
	if health.DeliveredBytes == 0 {
		t.Fatal("no data was delivered")
	}
	t.Logf("delivered %d bytes in total", health.DeliveredBytes)

	if err := session.UnmountISO(); err != nil {
		t.Fatalf("UnmountISO: %v", err)
	}
	if mounted, _, _, _ := session.MediaStatus(); mounted {
		t.Fatal("the image should be gone after unmounting")
	}
}

// TestLiveConsoleEncodingTrace captures the screen twice: once from the first
// full update and once after incremental updates have run. If the two differ in
// colour, the fault is in an encoding the incremental path uses rather than in
// the pixel format. Set FIRSTLIGHT_IDRAC_TRACE_DIR to keep the images.
func TestLiveConsoleEncodingTrace(t *testing.T) {
	cfg := liveConfig(t)
	if raw := os.Getenv("FIRSTLIGHT_IDRAC_RAW_ONLY"); raw != "" {
		cfg.Encodings = []int32{rfb.EncodingRaw, rfb.EncodingDesktop, rfb.EncodingLastRect}
		t.Log("offering Raw only")
	}
	seen := map[string]int{}
	var mu sync.Mutex
	base := cfg.Logf
	cfg.TraceDecoder = true
	cfg.Logf = func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if index := strings.Index(line, "encoding="); index >= 0 {
			mu.Lock()
			seen[line[index+len("encoding="):]]++
			mu.Unlock()
			return
		}
		base(format, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	session, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	dir := os.Getenv("FIRSTLIGHT_IDRAC_TRACE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	save := func(name string) {
		path := filepath.Join(dir, name)
		file, err := os.Create(path)
		if err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		defer file.Close()
		if err := png.Encode(file, session.Frame(nil)); err != nil {
			t.Fatalf("encode %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
	}
	waitFor := func(revision uint64, within time.Duration) {
		deadline := time.After(within)
		for session.Screen().Revision() < revision {
			if session.State().WaitingForApproval {
				t.Skip("the controller is holding the console for another session")
			}
			select {
			case <-session.Changed():
			case <-session.Done():
				t.Fatalf("session ended: %s", session.State().Disconnect)
			case <-deadline:
				t.Fatalf("only %d update(s) arrived, wanted %d",
					session.Screen().Revision(), revision)
			}
		}
	}

	waitFor(1, 30*time.Second)
	save("console-first.png")
	mu.Lock()
	firstPass := fmt.Sprint(seen)
	mu.Unlock()
	t.Logf("after the first update: %s", firstPass)

	// Nudge the pointer so the firmware has something to send incrementally.
	state := session.State()
	_ = session.SendMouse(state.Width/2, state.Height/2, 0, 0, state.Width, state.Height, 0, 0)
	waitFor(4, 45*time.Second)
	save("console-later.png")
	mu.Lock()
	for name, count := range seen {
		t.Logf("encoding %s: %d rectangle(s)", name, count)
	}
	mu.Unlock()
}

// TestLiveSeizeClosesOtherSessions opens two sessions, leaves the first one
// behind the way a crashed client would, and checks that a seizing connect
// clears it.
//
// The check stops at the web session. Closing one does not hand the console
// over on the spot: the controller keeps the console slot that session held
// for a while longer, which is precisely why seizing is never automatic.
func TestLiveSeizeClosesOtherSessions(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	stale, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect (stale): %v", err)
	}
	// Deliberately not closed: this is what a client that died leaves behind.
	staleID := stale.webSessionID
	if staleID == "" {
		t.Fatal("the client should know its own session id")
	}
	t.Logf("left session %s behind", staleID)

	seizing := cfg
	seizing.SeizeOtherSessions = true
	session, err := Connect(ctx, seizing)
	if err != nil {
		t.Fatalf("Connect (seizing): %v", err)
	}
	defer func() { _ = session.Close() }()

	others, err := session.OtherSessions(ctx)
	if err != nil {
		t.Fatalf("OtherSessions: %v", err)
	}
	for _, item := range others {
		if item.ID == staleID {
			t.Fatalf("session %s survived the seize: %+v", staleID, item)
		}
	}
	t.Logf("session %s is gone, %d other session(s) remain", staleID, len(others))

	// Leave the controller as tidy as possible for the next run.
	if closed, err := session.TakeOverConsole(ctx); err == nil && closed > 0 {
		t.Logf("cleaned up %d further session(s)", closed)
	}
}
