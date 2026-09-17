package idrac

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginServer stands in for an iDRAC that accepts one credential form.
func loginServer(t *testing.T, accept func(*http.Request) bool) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sessionPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !accept(r) {
			// This is what the firmware really answers: a 400 carrying its own
			// verdict rather than a 401.
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"authResult":99,"blockingTime":0}`))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "-http-session-", Value: "abc123"})
		w.Header().Set("XSRF-TOKEN", "0123456789abcdef0123456789abcdef")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"authResult":0}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(Options{Addr: strings.TrimPrefix(server.URL, "https://")})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client.http = &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test server
	}}
	return client
}

func TestLoginSatisfiesBothCredentialForms(t *testing.T) {
	cases := []struct {
		name   string
		accept func(*http.Request) bool
	}{
		{
			// Firmware 7.30 and later.
			name: "basic authentication only",
			accept: func(r *http.Request) bool {
				user, password, ok := r.BasicAuth()
				return ok && user == "operator" && password == "secret"
			},
		},
		{
			// Firmware before 7.30.
			name: "quoted credential headers only",
			accept: func(r *http.Request) bool {
				return r.Header.Get("user") == `"operator"` && r.Header.Get("password") == `"secret"`
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := loginServer(t, c.accept)
			if err := client.Login(context.Background(), "operator", "secret"); err != nil {
				t.Fatalf("Login: %v", err)
			}
			if !client.LoggedIn() {
				t.Fatal("the session did not open")
			}
			cookie, xsrf := client.credentials()
			if !strings.Contains(cookie, "-http-session-=abc123") || xsrf == "" {
				t.Fatalf("cookie=%q xsrf=%q", cookie, xsrf)
			}
		})
	}
}

func TestLoginReportsTheFirmwareVerdict(t *testing.T) {
	client := loginServer(t, func(*http.Request) bool { return false })
	err := client.Login(context.Background(), "operator", "wrong")
	if err == nil {
		t.Fatal("a rejected login reported success")
	}
	// HTTP 400 alone says nothing; authResult is the part an operator can act on.
	if !strings.Contains(err.Error(), "authResult 99") {
		t.Fatalf("error = %q, want the firmware verdict in it", err)
	}
}

func TestLoginNamesTheBlockingPeriod(t *testing.T) {
	answer := parseAuthResult([]byte(`{"authResult":1,"blockingTime":60}`))
	got := answer.describe(nil)
	if !strings.Contains(got, "authResult 1") || !strings.Contains(got, "60 seconds") {
		t.Fatalf("describe = %q", got)
	}
	if plain := parseAuthResult([]byte("not json")).describe([]byte("Bad Request")); plain != " Bad Request" {
		t.Fatalf("fallback = %q", plain)
	}
}
