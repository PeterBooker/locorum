// Package mcp implements a Model Context Protocol server that exposes
// Locorum's daemon as a curated set of tools to local AI agents.
//
// Locorum's MCP server is a thin shim: every tool call is forwarded to
// the daemon over the IPC socket. The daemon enforces the security
// boundary (per-site mutex, profile gating, scope checks). This package
// owns the protocol surface and the tool catalogue.
//
// MCP (https://spec.modelcontextprotocol.io) is JSON-RPC 2.0 over
// either stdio (one frame per line) or Streamable HTTP. Locorum ships
// stdio first; HTTP is a follow-on (see AGENTS-SUPPORT.md P2).
package mcp

import (
	"encoding/json"
	"errors"
)

// MCPProtocolVersion is the version we declare in the initialize
// response. Negotiated against the client's claimed version; we accept
// any version for now and just echo back our own.
const MCPProtocolVersion = "2024-11-05"

// ServerName is the user-visible name reported in initialize. Pinned
// so MCP clients (Claude Code, Cursor, Continue) can recognise the
// server in their config UIs.
const ServerName = "locorum"

// Wire-format constants. MCP is JSON-RPC 2.0; reusing the same code
// values as the daemon keeps round-trip error mapping trivial.
const (
	jsonRPCVersion = "2.0"

	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// request is one inbound MCP frame. Notifications (no id) and
// requests (with id) share the same wire shape; the server branches
// on whether ID is present.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is one outbound MCP frame. Exactly one of Result or Error
// is set on a reply; notifications never produce a response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// initializeResult is the fixed response shape MCP clients expect from
// initialize. Capabilities advertise which features we support — only
// "tools" today; resources/prompts can land in a follow-on.
type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Capabilities    serverCapabilities `json:"capabilities"`
	Instructions    string             `json:"instructions,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	// ListChanged tells the client whether the server may emit
	// `notifications/tools/list_changed`. We don't today (the tool
	// list is fixed at process start), so this is false.
	ListChanged bool `json:"listChanged"`
}

// toolListResult is the response shape for `tools/list`.
type toolListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

// toolDescriptor matches MCP's required tool shape: name, description,
// JSON Schema for input. Title is the optional human-friendly name.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// toolCallResult is the response shape for `tools/call`. Content is a
// list of content parts; we always return one text part containing the
// pretty-printed result (or an error string with isError=true).
type toolCallResult struct {
	Content []contentPart `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// errStop is the sentinel returned by the read loop when stdin is
// closed (the controlling MCP client has gone away). Caught at the
// top level to terminate cleanly.
var errStop = errors.New("mcp: stdin closed")
