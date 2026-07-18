package ilo

import "testing"

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		host    string
		port    uint16
		wantErr bool
	}{
		{name: "empty", input: "  ", host: "", port: 443},
		{name: "hostname", input: " ilo.example ", host: "ilo.example", port: 443},
		{name: "hostname and port", input: "ilo.example:8443", host: "ilo.example", port: 8443},
		{name: "bracketed IPv6", input: "[2001:db8::1]", host: "2001:db8::1", port: 443},
		{name: "bracketed IPv6 and port", input: "[2001:db8::1]:8443", host: "2001:db8::1", port: 8443},
		{name: "unbracketed IPv6", input: "2001:db8::1", host: "2001:db8::1", port: 443},
		{name: "invalid port", input: "ilo.example:not-a-port", wantErr: true},
		{name: "overflowing port", input: "ilo.example:65536", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host, port, err := ParseAddress(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseAddress(%q) err=%v wantErr=%v", test.input, err, test.wantErr)
			}
			if err == nil && (host != test.host || port != test.port) {
				t.Fatalf("ParseAddress(%q)=(%q,%d), want (%q,%d)", test.input, host, port, test.host, test.port)
			}
		})
	}
}

func TestNewClientBuildsIPv6SafeBaseURL(t *testing.T) {
	client, err := NewClient(Options{Addr: "[2001:db8::1]:8443"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := client.base.String(), "https://[2001:db8::1]:8443"; got != want {
		t.Fatalf("base URL=%q want=%q", got, want)
	}
}
