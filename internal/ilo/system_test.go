package ilo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetOneTimeBootUsesAuthenticatedSessionAndVerifies(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != computerSystemPath {
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-Auth-Token") != "existing-session" {
			t.Errorf("missing existing session token: %q", r.Header.Get("X-Auth-Token"))
		}
		calls++
		switch calls {
		case 1:
			_, _ = w.Write([]byte(`{"PowerState":"On","Boot":{"BootSourceOverrideTarget":"None","BootSourceOverrideEnabled":"Disabled","BootSourceOverrideMode":"UEFI","BootSourceOverrideTarget@Redfish.AllowableValues":["None","Cd"],"BootSourceOverrideEnabled@Redfish.AllowableValues":["Disabled","Once"]}}`))
		case 2:
			if r.Method != http.MethodPatch || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("update request method=%s headers=%v", r.Method, r.Header)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(body)
			if string(encoded) != `{"Boot":{"BootSourceOverrideEnabled":"Once","BootSourceOverrideTarget":"Cd"}}` {
				t.Errorf("unexpected PATCH body %s", encoded)
			}
			w.WriteHeader(http.StatusNoContent)
		case 3:
			_, _ = w.Write([]byte(`{"PowerState":"On","Boot":{"BootSourceOverrideTarget":"Cd","BootSourceOverrideEnabled":"Once","BootSourceOverrideMode":"UEFI"}}`))
		default:
			t.Errorf("unexpected extra request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := testHTTPClient(t, server)
	client.sessionKey = "existing-session"
	change, err := client.SetOneTimeBoot(context.Background(), " cd ")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || change.Device != "cd" || change.Before.Target != "None" ||
		change.Before.Mode != "UEFI" || change.Current.Target != "Cd" ||
		change.Current.Enabled != "Once" || change.Current.Mode != "UEFI" || !change.Verified {
		t.Fatalf("calls=%d change=%+v", calls, change)
	}
}

func TestManagementStatusUsesRedfishSystemResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"PowerState":"Off","Boot":{"BootSourceOverrideTarget":"Cd","BootSourceOverrideEnabled":"Once","BootSourceOverrideMode":"Legacy"}}`))
	}))
	defer server.Close()

	client := testHTTPClient(t, server)
	status, err := client.GetManagementStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.PowerState != "Off" || status.BootOverride.Target != "Cd" ||
		status.BootOverride.Enabled != "Once" || status.BootOverride.Mode != "Legacy" {
		t.Fatalf("status=%+v", status)
	}
}

func TestSetOneTimeBootReportsSafeUnsupportedAndPOSTBusyErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		want       error
		secretText string
	}{
		{name: "unsupported", status: http.StatusNotFound, body: `{"error":"secret-session-value"}`, want: ErrManagementUnsupported, secretText: "secret-session-value"},
		{name: "post busy", status: http.StatusConflict, body: `{"error":{"@Message.ExtendedInfo":[{"MessageId":"iLO.SystemPOSTInProgress","Message":"System POST is currently in progress at Z:\\\\private\\\\boot.iso"}]}}`, want: ErrSystemPOSTBusy, secretText: `Z:\private\boot.iso`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := testHTTPClient(t, server)
			_, err := client.SetOneTimeBoot(context.Background(), "cd")
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
			if err != nil && test.secretText != "" && containsErrorText(err, test.secretText) {
				t.Fatalf("safe error leaked response text: %q", err)
			}
		})
	}
}

func TestSetOneTimeBootRejectsUnsupportedDeviceBeforeNetwork(t *testing.T) {
	client := &Client{}
	if _, err := client.SetOneTimeBoot(context.Background(), "usb"); err == nil {
		t.Fatal("unsupported device was accepted")
	}
}

func containsErrorText(err error, text string) bool {
	return err != nil && text != "" && len(err.Error()) >= len(text) &&
		findSubstring(err.Error(), text)
}

func findSubstring(value, text string) bool {
	for i := 0; i+len(text) <= len(value); i++ {
		if value[i:i+len(text)] == text {
			return true
		}
	}
	return false
}
