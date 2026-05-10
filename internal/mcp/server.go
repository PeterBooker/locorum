package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/secrets"
)

// Server is the MCP stdio server. Constructed once per process; the
// caller wires stdin/stdout and a daemon client, then calls Serve.
//
// Server is safe to use from a single goroutine only. MCP stdio is
// one-frame-at-a-time, no pipelining required by the spec.
type Server struct {
	in     io.Reader
	out    io.Writer
	client *daemon.Client
	logger *slog.Logger

	// scope is the LOCORUM_MCP_SCOPE site identifier the parent set
	// at process start. Empty means "no scope; the agent may target
	// any site." Non-empty values are forwarded to the daemon at
	// hello time so the daemon — not the client — enforces scope.
	scope string

	// profile is the trust tier for this MCP server. Determines which
	// tools appear in tools/list and which the dispatcher refuses to
	// call. Sandbox is reserved (Part 6).
	profile string

	// version is the locorum version string, surfaced to MCP clients
	// in the initialize response.
	version string

	// initialized is set after the client sends notifications/initialized
	// per the MCP handshake. Tools/* calls are accepted regardless
	// (some clients call them eagerly), but we don't emit
	// notifications until the handshake completes.
	mu          sync.Mutex
	initialized bool
}

// Options configures a new MCP server.
type Options struct {
	In      io.Reader // defaults to os.Stdin
	Out     io.Writer // defaults to os.Stdout
	Client  *daemon.Client
	Logger  *slog.Logger
	Scope   string // optional MCP scope (LOCORUM_MCP_SCOPE)
	Profile string // "full" or "readonly"
	Version string // locorum binary version
}

// NewServer constructs a Server. The caller is responsible for opening
// the daemon client and passing it in: this package never auto-spawns
// a daemon (the parent process — `locorum mcp serve` — does that
// before instantiating us).
func NewServer(opts Options) *Server {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	profile := opts.Profile
	if profile == "" {
		profile = daemon.ProfileFull
	}
	return &Server{
		in:      in,
		out:     out,
		client:  opts.Client,
		logger:  logger.With("subsys", "mcp"),
		scope:   opts.Scope,
		profile: profile,
		version: opts.Version,
	}
}

// Serve runs the read/dispatch loop until stdin closes or ctx cancels.
// Returns nil on a clean stdin close; other errors are propagated.
func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	// MCP spec doesn't bound message size; reuse the daemon's 8 MiB
	// cap as a sane upper bound. A larger payload than this is almost
	// certainly a bug.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeError(nil, codeParseError, "parse error: "+err.Error())
			continue
		}
		if err := s.dispatch(ctx, req); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			s.logger.Warn("dispatch error", "method", req.Method, "err", err.Error())
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// dispatch handles one request/notification.
func (s *Server) dispatch(ctx context.Context, req request) error {
	if req.JSONRPC != "" && req.JSONRPC != jsonRPCVersion {
		s.writeError(req.ID, codeInvalidRequest, "unsupported jsonrpc version")
		return nil
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized":
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
		return nil
	case "ping":
		s.writeResult(req.ID, struct{}{})
		return nil
	case "tools/list":
		s.writeResult(req.ID, toolListResult{Tools: s.toolList()})
		return nil
	case "tools/call":
		return s.handleToolCall(ctx, req)
	default:
		// Notifications other than the ones we handle: silently
		// drop. Requests: respond with method-not-found.
		if len(req.ID) == 0 {
			return nil
		}
		s.writeError(req.ID, codeMethodNotFound, "method not found: "+req.Method)
		return nil
	}
}

// handleInitialize echoes the protocol handshake.
func (s *Server) handleInitialize(req request) error {
	instructions := "Locorum exposes site-management tools backed by a local daemon. " +
		"Mutating tools require Profile=full; readonly returns only inspection tools. " +
		"When MCP scope is set, every site-targeted tool is forced to that site by the daemon."
	s.writeResult(req.ID, initializeResult{
		ProtocolVersion: MCPProtocolVersion,
		ServerInfo:      serverInfo{Name: ServerName, Version: s.version},
		Capabilities:    serverCapabilities{Tools: &toolsCapability{ListChanged: false}},
		Instructions:    instructions,
	})
	return nil
}

// handleToolCall parses the tool name + arguments, runs the
// implementation, and writes a tools/call response.
func (s *Server) handleToolCall(ctx context.Context, req request) error {
	type toolCallParams struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
		return nil
	}

	tool, ok := s.findTool(p.Name)
	if !ok {
		s.writeError(req.ID, codeMethodNotFound, "unknown tool: "+p.Name)
		return nil
	}
	if tool.requireFull && s.profile != daemon.ProfileFull {
		s.writeToolError(req.ID, fmt.Sprintf("tool %q requires the full profile (current: %s)", p.Name, s.profile))
		return nil
	}

	out, err := tool.impl(ctx, s, p.Arguments)
	if err != nil {
		// Tool errors translate to isError=true (per MCP spec) so the
		// agent sees the message inline rather than treating it as a
		// transport failure.
		s.writeToolError(req.ID, err.Error())
		return nil
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		s.writeError(req.ID, codeInternalError, "marshal tool result: "+err.Error())
		return nil
	}
	s.writeResult(req.ID, toolCallResult{
		Content: []contentPart{{Type: "text", Text: string(body)}},
	})
	return nil
}

// writeResult encodes a successful response on stdout. MCP clients
// expect one frame per line; we use json.Encoder which adds a newline.
func (s *Server) writeResult(id json.RawMessage, result any) {
	body, err := json.Marshal(result)
	if err != nil {
		s.writeError(id, codeInternalError, "marshal result: "+err.Error())
		return
	}
	resp := response{JSONRPC: jsonRPCVersion, ID: id, Result: body}
	s.write(resp)
}

// writeError encodes a transport-level error. Messages are passed through
// secrets.RedactString so a backend error that echoes a DB password into
// its message — e.g. a Docker exec error reflecting argv — is sanitised
// before reaching the MCP client.
func (s *Server) writeError(id json.RawMessage, code int, msg string) {
	resp := response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error:   &rpcError{Code: code, Message: secrets.RedactString(msg)},
	}
	s.write(resp)
}

// writeToolError encodes a tool-level error inside a successful
// response (isError=true). MCP separates these so the agent can
// recover from a tool failure without treating it as a fatal RPC bug.
// Same redaction discipline as writeError.
func (s *Server) writeToolError(id json.RawMessage, msg string) {
	body, _ := json.Marshal(toolCallResult{
		Content: []contentPart{{Type: "text", Text: secrets.RedactString(msg)}},
		IsError: true,
	})
	resp := response{JSONRPC: jsonRPCVersion, ID: id, Result: body}
	s.write(resp)
}

// write encodes a frame on stdout, holding a mutex so a slow producer
// goroutine cannot interleave bytes with a notification.
func (s *Server) write(resp response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enc := json.NewEncoder(s.out)
	if err := enc.Encode(resp); err != nil {
		s.logger.Warn("write frame failed", "err", err.Error())
	}
}

// callDaemon is a convenience that proxies an IPC call. Tools use it
// rather than reaching into s.client directly so future cross-cutting
// behaviour (telemetry, retry on disconnect) lands in one place.
func (s *Server) callDaemon(ctx context.Context, method string, params, out any) error {
	return s.client.Call(ctx, method, params, out)
}
