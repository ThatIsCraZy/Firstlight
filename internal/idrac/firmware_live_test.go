package idrac

import (
	"context"
	"slices"
	"testing"
	"time"
)

// Compare the management operations Firstlight uses with the live firmware's
// advertised contract. This does not reset the host or change its boot order.
func TestLiveManagementCapabilitiesMatchPowerAndBootActions(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client, err := NewClient(Options{Addr: cfg.Addr, VerifyCert: cfg.VerifyCert})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Login(ctx, cfg.User, cfg.Password); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() {
		logoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Logout(logoutCtx); err != nil {
			t.Errorf("Logout: %v", err)
		}
	}()

	var system struct {
		Actions struct {
			Reset struct {
				Target string   `json:"target"`
				Values []string `json:"ResetType@Redfish.AllowableValues"`
			} `json:"#ComputerSystem.Reset"`
		} `json:"Actions"`
		Boot struct {
			Targets []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
		} `json:"Boot"`
	}
	if err := client.getJSON(ctx, systemPath, &system); err != nil {
		t.Fatalf("read management capabilities: %v", err)
	}
	if target := system.Actions.Reset.Target; target != resetAction {
		t.Errorf("reset endpoint = %q, client uses %q", target, resetAction)
	}
	for _, action := range []PowerAction{PowerMomentaryPress, PowerPressAndHold, PowerColdBootAction, PowerResetAction} {
		for _, powerOn := range []bool{false, true} {
			_, reset, err := powerMapping(action, powerOn)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(system.Actions.Reset.Values, string(reset)) {
				t.Errorf("%s (power on=%v) needs %s, firmware advertises %v",
					action, powerOn, reset, system.Actions.Reset.Values)
			}
		}
	}
	for _, device := range []string{"cd", "pxe", "hdd", "bios", "usb"} {
		target, err := bootTarget(device)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(system.Boot.Targets, target) {
			t.Errorf("boot device %s needs %s, firmware advertises %v", device, target, system.Boot.Targets)
		}
	}
	t.Logf("advertised reset types: %v; boot targets: %v", system.Actions.Reset.Values, system.Boot.Targets)
}

// Firmware must release each console slot after an ordinary close; otherwise
// the next viewer may receive no video despite a successful handshake.
func TestLiveConsoleCloseReleasesSlot(t *testing.T) {
	cfg := liveConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := NewClient(Options{Addr: cfg.Addr, VerifyCert: cfg.VerifyCert})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Login(ctx, cfg.User, cfg.Password); err != nil {
		t.Fatalf("Login: %v", err)
	}
	defer func() {
		logoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Logout(logoutCtx); err != nil {
			t.Errorf("Logout: %v", err)
		}
	}()
	settings, err := client.ConsoleSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ActiveSessions != 0 {
		t.Skip("another console is active; leave its session untouched")
	}
	for i := range 3 {
		session, err := Connect(ctx, cfg)
		if err != nil {
			t.Fatalf("connect %d: %v", i+1, err)
		}
		t.Cleanup(func() { _ = session.Close() })
		deadline := time.Now().Add(20 * time.Second)
		for session.Screen().Revision() == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("console %d did not deliver video", i+1)
			}
			if err := sleepContext(ctx, 100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
		if err := session.Close(); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
		deadline = time.Now().Add(10 * time.Second)
		for {
			settings, err := client.ConsoleSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if settings.ActiveSessions == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("console %d left %d active slot(s) after closing", i+1, settings.ActiveSessions)
			}
			if err := sleepContext(ctx, 250*time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("console %d delivered video and released its slot", i+1)
	}
}
