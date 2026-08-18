package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"firstlight/internal/console"
	"firstlight/internal/mcpserver"
)

func TestValidateLoopbackListen(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8765", "[::1]:8765", "localhost:8765"} {
		if err := validateLoopbackListen(address); err != nil {
			t.Errorf("validateLoopbackListen(%q): %v", address, err)
		}
	}
}

func TestValidateLoopbackListenRejectsRemoteExposure(t *testing.T) {
	for _, address := range []string{":8765", "0.0.0.0:8765", "192.0.2.20:8765", "ilo.example.test:8765"} {
		if err := validateLoopbackListen(address); err == nil {
			t.Errorf("validateLoopbackListen(%q) accepted a non-loopback address", address)
		}
	}
}

func TestMCPHTTPHandlerIsStateless(t *testing.T) {
	manager := console.NewManager(context.Background(), console.ManagerOptions{})
	defer manager.Close()
	httpServer := httptest.NewServer(newMCPHTTPHandler(mcpserver.New(manager)))
	defer httpServer.Close()

	requestBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`)
	for range 2 {
		request, err := http.NewRequest(http.MethodPost, httpServer.URL, bytes.NewReader(requestBody))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2026-07-28")
		request.Header.Set("Mcp-Method", "tools/list")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %s", response.StatusCode, body)
		}
		if response.Header.Get("Mcp-Session-Id") != "" {
			t.Fatalf("stateless handler returned Mcp-Session-Id %q", response.Header.Get("Mcp-Session-Id"))
		}
		if !strings.Contains(string(body), `"ilo_console_open"`) {
			t.Fatalf("tools/list response does not contain ilo_console_open: %s", body)
		}
	}
}
