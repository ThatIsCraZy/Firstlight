package idrac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"firstlight/internal/rfb"
)

// Exercise the same session entry point used by both the desktop and MCP
// backends, and inspect the command that actually reaches the console socket.
func TestMomentaryPressPowersOnAnOffHost(t *testing.T) {
	s, socket := keyboardFixture(t)
	s.handleControl([]byte{247, MsgHostPowerState, 0})

	if err := s.Power(context.Background(), PowerMomentaryPress); err != nil {
		t.Fatalf("Momentary Press: %v", err)
	}
	want := []byte{247, 0, 0xF1, 4} // Dell power-on request.
	if len(socket.written) != 1 || !bytes.Equal(socket.written[0], want) {
		t.Fatalf("Momentary Press on an off host sent % x, want power-on % x", socket.written, want)
	}
}

func TestMomentaryPressFollowsConsolePowerState(t *testing.T) {
	s, socket := keyboardFixture(t)
	for i, powerOn := range []byte{1, 0, 1} {
		s.handleControl([]byte{247, MsgHostPowerState, powerOn})
		if err := s.Power(context.Background(), " MOMENTARY_PRESS "); err != nil {
			t.Fatalf("Momentary Press: %v", err)
		}
		operation := byte(0) // Graceful shutdown while on.
		if powerOn == 0 {
			operation = 4 // Power on while off.
		}
		want := []byte{247, 0, 0xF1, operation}
		if len(socket.written) != i+1 || !bytes.Equal(socket.written[i], want) {
			t.Fatalf("power state %d: sent % x, want % x", powerOn, socket.written, want)
		}
	}
}

func powerTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Options{Addr: strings.TrimPrefix(server.URL, "https://")})
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	return client
}

func TestMomentaryPressReadsMissingPowerState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		status    int
		operation byte
		wantError string
	}{
		{name: "off", state: "Off", operation: 4},
		{name: "on", state: "On", operation: 0},
		{name: "missing state", wantError: "power state"},
		{name: "transition", state: "PoweringOn", wantError: "power state"},
		{name: "unavailable", status: http.StatusServiceUnavailable, wantError: "503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, socket := keyboardFixture(t)
			var reads atomic.Int32
			s.client = powerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != systemPath {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				reads.Add(1)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"PowerState": tc.state})
			})

			err := s.Power(context.Background(), PowerMomentaryPress)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				if len(socket.written) != 0 {
					t.Fatalf("sent a power command without a known state: % x", socket.written)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				want := []byte{247, 0, 0xF1, tc.operation}
				if len(socket.written) != 1 || !bytes.Equal(socket.written[0], want) {
					t.Fatalf("sent % x, want % x", socket.written, want)
				}
			}
			if got := reads.Load(); got != 1 {
				t.Fatalf("power status reads = %d, want 1", got)
			}
		})
	}
}

type failedPowerWS struct{ *scriptedWS }

func (*failedPowerWS) WriteMessage([]byte) error { return errors.New("console write failed") }

func TestMomentaryPressRedfishFallbackMatchesPowerState(t *testing.T) {
	for _, tc := range []struct {
		state byte
		reset string
	}{
		{state: 0, reset: "On"},
		{state: 1, reset: "GracefulShutdown"},
	} {
		t.Run(tc.reset, func(t *testing.T) {
			s, socket := keyboardFixture(t)
			s.handleControl([]byte{247, MsgHostPowerState, tc.state})
			s.trans = newTransport(&failedPowerWS{socket}, nil)
			s.rfb = rfb.New(s.trans)
			var resets atomic.Int32
			s.client = powerTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != resetAction {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var body struct{ ResetType string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("reset request: %v", err)
				}
				if body.ResetType != tc.reset {
					t.Errorf("ResetType = %q, want %q", body.ResetType, tc.reset)
				}
				resets.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
			if err := s.Power(context.Background(), PowerMomentaryPress); err != nil {
				t.Fatal(err)
			}
			if got := resets.Load(); got != 1 {
				t.Fatalf("reset requests = %d, want 1", got)
			}
		})
	}
}
