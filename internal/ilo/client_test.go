package ilo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClientLoginAuthenticatedRequestAndLogout(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/json/login_session":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if body["method"] == "login" {
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("login content type=%q", r.Header.Get("Content-Type"))
				}
				_, _ = w.Write([]byte(`{"session_key":"session-123"}`))
				return
			}
			if body["method"] != "logout" || body["session_key"] != "session-123" || r.Header.Get("X-Auth-Token") != "session-123" {
				t.Errorf("bad logout request body=%v token=%q", body, r.Header.Get("X-Auth-Token"))
			}
			w.WriteHeader(http.StatusNoContent)
		case "/redfish/v1/Managers/1/RcInfo/":
			if r.Header.Get("Accept") != "application/json" || r.Header.Get("X-Auth-Token") != "session-123" {
				t.Errorf("bad authenticated headers: %v", r.Header)
			}
			w.WriteHeader(http.StatusNotFound)
		case "/json/rc_info":
			_, _ = w.Write([]byte(`{"enc_key":"00112233445566778899aabbccddeeff","rc_port":17990,"vm_port":17988,"protocol_version":2}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := testHTTPClient(t, server)
	session, err := client.Login(context.Background(), "Administrator", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if session.Key != "session-123" || client.SessionKey() != "session-123" {
		t.Fatalf("session=%+v clientKey=%q", session, client.SessionKey())
	}
	rc, err := client.GetRCInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Source != "legacy" || rc.RCPort != 17990 {
		t.Fatalf("rcinfo=%+v", rc)
	}
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.SessionKey() != "" {
		t.Fatalf("logout kept session key %q", client.SessionKey())
	}
	if len(calls) != 4 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestClientReportsHTTPAndJSONErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/http-error" {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("short and stout"))
			return
		}
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()
	client := testHTTPClient(t, server)

	var out map[string]any
	err := client.getJSON(context.Background(), "/http-error", &out)
	var httpErr HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusTeapot || httpErr.Body != "short and stout" {
		t.Fatalf("HTTP error=%#v", err)
	}
	err = client.getJSON(context.Background(), "/bad-json", &out)
	if err == nil || !strings.HasPrefix(err.Error(), "/bad-json") {
		t.Fatalf("JSON error=%v", err)
	}
}

func testHTTPClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{base: base, http: server.Client()}
}
