package mcpserver

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGuardTurnsAPanicIntoAToolError(t *testing.T) {
	// The guard logs the stack; keep it out of the test output.
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
		panic("the handler hit a bug")
	}
	result, output, err := guard(handler)(context.Background(), nil, struct{}{})
	if err == nil {
		t.Fatal("the panic escaped instead of becoming a tool error")
	}
	if result != nil || output != "" {
		t.Fatalf("a failed call returned content: result=%v output=%q", result, output)
	}
}

func TestGuardPassesAnOrdinaryCallThrough(t *testing.T) {
	want := errors.New("the console is closed")
	handler := func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, string, error) {
		return nil, "done", want
	}
	_, output, err := guard(handler)(context.Background(), nil, struct{}{})
	if output != "done" || !errors.Is(err, want) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
