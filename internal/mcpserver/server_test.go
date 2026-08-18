package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"ilo-kvm/internal/console"
)

func TestServerPublishesExpectedCredentialBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager := console.NewManager(ctx, console.ManagerOptions{})
	defer manager.Close()
	server := New(manager)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	}()

	result, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"ilo_console_open":                 false,
		"ilo_console_observe":              false,
		"ilo_console_type_text":            false,
		"ilo_console_press_keys":           false,
		"ilo_console_mouse":                false,
		"ilo_console_power":                false,
		"ilo_console_management_status":    false,
		"ilo_console_set_one_time_boot":    false,
		"ilo_console_close":                false,
		"ilo_console_mount_iso":            false,
		"ilo_console_virtual_media_status": false,
		"ilo_console_unmount_iso":          false,
	}
	var openTool *mcp.Tool
	tools := make(map[string]*mcp.Tool)
	for _, tool := range result.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		want[tool.Name] = true
		tools[tool.Name] = tool
		if tool.Name == "ilo_console_open" {
			openTool = tool
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q is missing", name)
		}
	}
	if openTool == nil {
		t.Fatal("ilo_console_open is missing")
	}
	schema, err := json.Marshal(openTool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"address", "username", "password", "operation_id"} {
		if !strings.Contains(string(schema), `"`+field+`"`) {
			t.Errorf("open input schema does not contain %q: %s", field, schema)
		}
	}
	if strings.Contains(strings.ToLower(openTool.Description), "credential store") == false {
		t.Errorf("open tool does not document its credential-store boundary: %q", openTool.Description)
	}
	for name, fields := range map[string][]string{
		"ilo_console_observe":              {"console_handle", "after_revision", "wait_ms"},
		"ilo_console_management_status":    {"console_handle"},
		"ilo_console_set_one_time_boot":    {"operation_id", "console_handle", "device", "confirm"},
		"ilo_console_mount_iso":            {"operation_id", "console_handle", "iso_path", "confirm"},
		"ilo_console_virtual_media_status": {"console_handle"},
		"ilo_console_unmount_iso":          {"operation_id", "console_handle", "confirm"},
	} {
		tool := tools[name]
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range fields {
			if !strings.Contains(string(schema), `"`+field+`"`) {
				t.Errorf("%s schema does not contain %q: %s", name, field, schema)
			}
		}
	}
	if tools["ilo_console_virtual_media_status"].Annotations == nil || !tools["ilo_console_virtual_media_status"].Annotations.ReadOnlyHint {
		t.Error("virtual media status must be advertised as read-only")
	}
	if tools["ilo_console_management_status"].Annotations == nil || !tools["ilo_console_management_status"].Annotations.ReadOnlyHint {
		t.Error("management status must be advertised as read-only")
	}
	if !strings.Contains(strings.ToLower(tools["ilo_console_set_one_time_boot"].Description), "never resets") {
		t.Errorf("one-time boot tool does not document the no-reset boundary: %q", tools["ilo_console_set_one_time_boot"].Description)
	}
	for _, name := range []string{"ilo_console_set_one_time_boot", "ilo_console_mount_iso", "ilo_console_unmount_iso"} {
		annotations := tools[name].Annotations
		if annotations == nil || annotations.DestructiveHint == nil || !*annotations.DestructiveHint {
			t.Errorf("%s must be advertised as destructive", name)
		}
	}
}
