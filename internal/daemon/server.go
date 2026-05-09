package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Handler is the per-method implementation. Receives the connection
// context (already wired with cancellation), the unparsed params
// (json.RawMessage), and returns either a result that will be JSON-
// encoded as the response, or a *MethodError for typed errors.
//
// Handlers may return any error type; non-MethodError values map to
// codeInternalError with the error string as the message. Sensitive
// data (file paths the user might prefer hidden, plaintext credentials)
// must NOT be embedded in handler errors — clients log them.
type Handler func(ctx context.Context, conn *Conn, params json.RawMessage) (any, error)

// Conn is the per-connection metadata exposed to handlers. PeerKind is
// the self-declared client identity (cli, mcp, gui) — useful for
// telemetry and "agent ran 17 mutations in the last hour" counters.
// MCPScope, when non-empty, restricts every handler call to a specific
// site slug; the dispatcher enforces this for site-scoped methods so
// rogue MCP tool calls cannot pivot to a sibling worktree (D6 in the
// AGENTS-SUPPORT plan).
type Conn struct {
	PeerKind string
	MCPScope string
	// Profile names the trust tier the client has selected. "full" is
	// the default; "readonly" restricts to a curated method allowlist.
	// Sandbox is reserved for a future tier (Part 6).
	Profile string

	// remote is the underlying connection. Handlers don't read or
	// write it directly; the server owns the framing.
	remote net.Conn
}

// Server is the JSON-RPC server side of the daemon. Constructed once
// per process, mounted on a Listener, owns goroutine lifecycle for
// every accepted connection.
type Server struct {
	ln       Listener
	handlers map[string]methodEntry
	logger   *slog.Logger

	// activeConns counts in-flight connections so Shutdown can wait
	// for them to drain. atomic to avoid a mutex on every Accept.
	activeConns int64

	// wg tracks every accept-loop and per-conn goroutine so Shutdown
	// can return only after all of them exit.
	wg sync.WaitGroup

	// shutdownOnce guards against a double-close: a defer-Shutdown in
	// the top-level call site combined with an explicit Shutdown in a
	// signal handler is realistic.
	shutdownOnce sync.Once
	shutdownCh   chan struct{}
}

// methodEntry pairs a handler with its profile gating metadata. ReadOnly
// methods are reachable from the readonly profile; everything else is
// full-only. SiteScoped methods carry a "siteId" or "slug" string field
// in their Params; the dispatcher pulls that field and rejects the call
// if it doesn't match Conn.MCPScope (when scope is set).
type methodEntry struct {
	handler    Handler
	readOnly   bool
	siteScoped bool
}

// NewServer constructs a Server bound to ln. Methods are registered via
// Register; the caller does that before Serve.
func NewServer(ln Listener, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		ln:         ln,
		handlers:   make(map[string]methodEntry),
		logger:     logger.With("subsys", "ipc"),
		shutdownCh: make(chan struct{}),
	}
}

// Register installs a handler for a method name. ReadOnly methods are
// reachable from the readonly profile. SiteScoped methods accept Params
// containing either a "siteId" or "slug" string; when MCP-scoped, the
// dispatcher rejects calls whose value differs from the conn scope.
func (s *Server) Register(method string, h Handler, opts ...MethodOption) {
	entry := methodEntry{handler: h}
	for _, opt := range opts {
		opt(&entry)
	}
	s.handlers[method] = entry
}

// MethodOption mutates a methodEntry at registration time.
type MethodOption func(*methodEntry)

// ReadOnly marks a method as available in the readonly profile.
func ReadOnly() MethodOption { return func(m *methodEntry) { m.readOnly = true } }

// SiteScoped marks a method as enforcing scope checks against
// Conn.MCPScope. Params must be a JSON object with a "siteId" or
// "slug" string field; missing or mismatched values reject the call
// with CodeForbidden.
func SiteScoped() MethodOption { return func(m *methodEntry) { m.siteScoped = true } }

// Serve runs the accept loop until the listener is closed or Shutdown
// is called. Blocks; callers run it in a goroutine.
func (s *Server) Serve(ctx context.Context) error {
	s.wg.Add(1)
	defer s.wg.Done()

	// Cancel-on-shutdown ctx so per-conn handlers see the signal even
	// before their underlying connection is closed.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-s.shutdownCh
		cancel()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || isShutdown(s.shutdownCh) {
				return nil
			}
			// Transient accept errors (e.g. EMFILE) shouldn't kill
			// the daemon; log and back off briefly.
			s.logger.Warn("accept failed", "err", err.Error())
			select {
			case <-time.After(100 * time.Millisecond):
			case <-s.shutdownCh:
				return nil
			}
			continue
		}
		atomic.AddInt64(&s.activeConns, 1)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer atomic.AddInt64(&s.activeConns, -1)
			s.handleConn(ctx, conn)
		}()
	}
}

// Shutdown closes the listener, signals every in-flight connection,
// and waits for handlers to drain (or up to timeout). Idempotent.
//
// Shutdown does NOT cancel handlers mid-RPC by force-closing the
// connection — handlers see ctx.Done() and may complete or abort
// gracefully. After the timeout, remaining connections are closed.
func (s *Server) Shutdown(timeout time.Duration) {
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
		_ = s.ln.Close()
	})
	if timeout <= 0 {
		s.wg.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		s.logger.Warn("shutdown timed out with active connections",
			"active", atomic.LoadInt64(&s.activeConns))
	}
}

// handleConn reads newline-delimited JSON-RPC frames from conn and
// dispatches each through the registered handler. One inbound message
// produces exactly one outbound message; pipelining is supported (the
// server reads the next frame as soon as the response is written).
func (s *Server) handleConn(ctx context.Context, raw net.Conn) {
	defer func() { _ = raw.Close() }()

	conn := &Conn{
		Profile: ProfileFull,
		remote:  raw,
	}

	// One reader per conn. bufio.Scanner is bounded by MaxMessageBytes
	// to keep a single hostile client from claiming all the daemon's
	// memory by sending a multi-gigabyte never-newlined frame.
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageBytes)
	// Custom split: standard ScanLines is fine, but we want to trim CR
	// proactively for any cross-platform clients that send CRLF.
	scanner.Split(bufio.ScanLines)

	enc := json.NewEncoder(raw)

	// Connection write mutex: response writes from the dispatcher and
	// future server-pushes (activity stream) share the connection.
	var writeMu sync.Mutex

	// Per-connection ctx so a malformed peer disconnect cancels its
	// own in-flight handlers without affecting others.
	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy the line — Scanner reuses its buffer. We hand the
		// bytes to a goroutine for concurrent dispatch, so the bytes
		// must outlive the next call to Scan.
		buf := make([]byte, len(line))
		copy(buf, line)

		var req Request
		if err := json.Unmarshal(buf, &req); err != nil {
			writeMu.Lock()
			_ = enc.Encode(Response{
				JSONRPC: jsonRPCVersion,
				Error: &RPCError{
					Code:    codeParseError,
					Message: "parse error: " + err.Error(),
				},
			})
			writeMu.Unlock()
			continue
		}

		// Dispatch happens inline (one in-flight RPC per connection),
		// not in a goroutine. JSON-RPC 2.0 allows pipelining but most
		// clients don't, and serialising avoids the response-ordering
		// surprise: a slow `start_site` followed by a fast
		// `list_sites` should not let the second response overtake
		// the first on the same conn.
		s.dispatch(connCtx, conn, req, &writeMu, enc)

		if isShutdown(s.shutdownCh) {
			return
		}
	}

	// scanner.Err returns nil on EOF (the normal client-disconnect
	// path). Any other error is logged at debug because a hostile
	// peer that overruns MaxMessageBytes shouldn't make noise in
	// production logs.
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.logger.Debug("conn read err", "err", err.Error())
	}
}

// dispatch resolves the method, enforces profile + scope gating, calls
// the handler, and writes the response.
func (s *Server) dispatch(ctx context.Context, conn *Conn, req Request, mu *sync.Mutex, enc *json.Encoder) {
	resp := Response{JSONRPC: jsonRPCVersion, ID: req.ID}

	if req.JSONRPC != "" && req.JSONRPC != jsonRPCVersion {
		resp.Error = &RPCError{Code: codeInvalidRequest, Message: "unsupported jsonrpc version"}
		writeResponse(mu, enc, resp)
		return
	}

	// Built-in methods: client.hello (declares peer kind / scope /
	// profile) is intercepted before the handler map so clients can
	// initialise their own metadata. server.shutdown is intentionally
	// NOT exposed over IPC — only the GUI's window-close path drives
	// shutdown today.
	switch req.Method {
	case "client.hello":
		s.handleHello(conn, req, mu, enc)
		return
	case "server.info":
		s.handleServerInfo(conn, req, mu, enc)
		return
	}

	entry, ok := s.handlers[req.Method]
	if !ok {
		resp.Error = &RPCError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
		writeResponse(mu, enc, resp)
		return
	}
	if conn.Profile == ProfileReadOnly && !entry.readOnly {
		resp.Error = &RPCError{Code: CodeForbidden, Message: "method not permitted in readonly profile: " + req.Method}
		writeResponse(mu, enc, resp)
		return
	}
	if entry.siteScoped && conn.MCPScope != "" {
		if err := enforceScope(conn.MCPScope, req.Params); err != nil {
			resp.Error = &RPCError{Code: CodeForbidden, Message: err.Error()}
			writeResponse(mu, enc, resp)
			return
		}
	}

	// Handler invocation. We catch panics so a bug in one method
	// doesn't bring down the daemon — log + return CodeInternalError.
	result, err := func() (out any, retErr error) {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("handler panic", "method", req.Method, "panic", fmt.Sprint(r))
				retErr = NewMethodError(codeInternalError, "internal error", nil)
			}
		}()
		return entry.handler(ctx, conn, req.Params)
	}()
	if err != nil {
		var me *MethodError
		if errors.As(err, &me) {
			resp.Error = &RPCError{Code: me.Code, Message: me.Error()}
		} else {
			resp.Error = &RPCError{Code: codeInternalError, Message: err.Error()}
		}
		writeResponse(mu, enc, resp)
		return
	}

	body, err := json.Marshal(result)
	if err != nil {
		resp.Error = &RPCError{Code: codeInternalError, Message: "marshal result: " + err.Error()}
		writeResponse(mu, enc, resp)
		return
	}
	resp.Result = body
	writeResponse(mu, enc, resp)
}

// handleHello processes the client.hello handshake. The client
// declares its kind / profile / scope; the daemon stamps the conn
// metadata and replies with server.info.
func (s *Server) handleHello(conn *Conn, req Request, mu *sync.Mutex, enc *json.Encoder) {
	type helloParams struct {
		PeerKind string `json:"peerKind"`
		Profile  string `json:"profile,omitempty"`
		MCPScope string `json:"mcpScope,omitempty"`
	}
	var p helloParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeResponse(mu, enc, Response{
				JSONRPC: jsonRPCVersion, ID: req.ID,
				Error: &RPCError{Code: codeInvalidParams, Message: "hello: " + err.Error()},
			})
			return
		}
	}
	conn.PeerKind = p.PeerKind
	if p.Profile != "" {
		switch p.Profile {
		case ProfileFull, ProfileReadOnly:
			conn.Profile = p.Profile
		default:
			writeResponse(mu, enc, Response{
				JSONRPC: jsonRPCVersion, ID: req.ID,
				Error: &RPCError{Code: codeInvalidParams, Message: "unknown profile: " + p.Profile},
			})
			return
		}
	}
	conn.MCPScope = p.MCPScope

	body, _ := json.Marshal(serverInfo{Version: jsonRPCVersion, Profile: conn.Profile})
	writeResponse(mu, enc, Response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: body})
}

// handleServerInfo returns daemon metadata so a CLI can verify it's
// talking to a compatible daemon version. Profile is always reported as
// the conn's effective profile.
func (s *Server) handleServerInfo(conn *Conn, req Request, mu *sync.Mutex, enc *json.Encoder) {
	body, _ := json.Marshal(serverInfo{Version: jsonRPCVersion, Profile: conn.Profile})
	writeResponse(mu, enc, Response{JSONRPC: jsonRPCVersion, ID: req.ID, Result: body})
}

type serverInfo struct {
	Version string `json:"version"`
	Profile string `json:"profile"`
}

// Profile names the trust tier a connection has selected. Wire-stable
// so MCP clients can pin them.
const (
	ProfileFull     = "full"
	ProfileReadOnly = "readonly"
)

// enforceScope is the dispatcher-side gate for SiteScoped methods. We
// tolerate either a "siteId" string (the canonical UUID) or a "slug"
// string (human-friendly) — both are matched against the scope, which
// itself may be either form. The MCP scope contract (D6 in the plan)
// requires the daemon — not the client — to enforce this.
func enforceScope(scope string, params json.RawMessage) error {
	if len(params) == 0 {
		return errors.New("mcp scope: method requires site id but params are empty")
	}
	var holder struct {
		SiteID string `json:"siteId"`
		Slug   string `json:"slug"`
	}
	if err := json.Unmarshal(params, &holder); err != nil {
		return errors.New("mcp scope: params must include siteId or slug")
	}
	switch {
	case holder.SiteID != "" && holder.SiteID == scope:
		return nil
	case holder.Slug != "" && holder.Slug == scope:
		return nil
	}
	return fmt.Errorf("mcp scope: requested %q does not match conn scope %q",
		firstNonEmpty(holder.SiteID, holder.Slug), scope)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// writeResponse encodes resp under mu so concurrent dispatchers on the
// same conn don't interleave bytes on the wire.
func writeResponse(mu *sync.Mutex, enc *json.Encoder, resp Response) {
	mu.Lock()
	defer mu.Unlock()
	_ = enc.Encode(resp)
}

// isShutdown reports whether the shutdown channel has been closed.
// Cheap non-blocking peek used in the read/accept loops.
func isShutdown(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
