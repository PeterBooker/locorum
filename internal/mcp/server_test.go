package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/daemon"
)

// TestServer_ToolListReadOnly verifies the readonly profile filters out
// every mutating tool from the catalogue. This is the load-bearing
// guarantee of the readonly profile: an agent dropped into a readonly
// MCP can't even discover that mutation tools exist.
func TestServer_ToolListReadOnly(t *testing.T) {
	srv := NewServer(Options{
		Profile: daemon.ProfileReadOnly,
		Version: "test",
	})
	tools := srv.toolList()
	for _, t := range tools {
		switch t.Name {
		case "start_site", "stop_site", "wp_cli",
			"create_snapshot", "restore_snapshot", "run_hook":
			panic("readonly profile leaked mutating tool: " + t.Name)
		}
	}
	if len(tools) == 0 {
		t.Fatalf("readonly profile exposes no tools")
	}
}

// TestServer_ToolListFull covers the full profile: every registered
// tool should appear, including mutators.
func TestServer_ToolListFull(t *testing.T) {
	srv := NewServer(Options{
		Profile: daemon.ProfileFull,
		Version: "test",
	})
	tools := srv.toolList()
	names := map[string]bool{}
	for _, t := range tools {
		names[t.Name] = true
	}
	for _, want := range []string{"list_sites", "describe_site", "start_site", "wp_cli", "create_snapshot"} {
		if !names[want] {
			t.Fatalf("full profile missing tool: %s", want)
		}
	}
}

// TestServer_InitializeHandshake exercises the JSON-RPC handshake using
// in-memory pipes. Confirms the initialize response shape MCP clients
// rely on.
func TestServer_InitializeHandshake(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out bytes.Buffer
	srv := NewServer(Options{
		In:      in,
		Out:     &out,
		Profile: daemon.ProfileReadOnly,
		Version: "test-1.2.3",
	})

	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response (%q): %v", out.String(), err)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc: %q", resp.JSONRPC)
	}
	if resp.ID != 1 {
		t.Fatalf("id: %d", resp.ID)
	}
	if !strings.Contains(string(resp.Result), `"name":"locorum"`) {
		t.Fatalf("result missing server name: %s", resp.Result)
	}
	if !strings.Contains(string(resp.Result), `"version":"test-1.2.3"`) {
		t.Fatalf("result missing version: %s", resp.Result)
	}
}

// TestServer_ToolsListShape confirms tools/list returns a plausible
// JSON Schema for a known tool.
func TestServer_ToolsListShape(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	srv := NewServer(Options{
		In:      in,
		Out:     &out,
		Profile: daemon.ProfileFull,
		Version: "test",
	})
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, out.String())
	}
	if len(resp.Result.Tools) == 0 {
		t.Fatalf("tools/list returned empty list")
	}
	for _, tool := range resp.Result.Tools {
		// Each tool must have a non-empty input schema (an object,
		// possibly with no properties).
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatalf("tool %q schema is not valid JSON: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("tool %q schema must be type=object, got %v", tool.Name, schema["type"])
		}
	}
}
