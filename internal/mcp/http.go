package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTP MCP transport. MCP defines a "Streamable HTTP" transport that
// accepts a JSON-RPC request via POST /mcp and returns either a
// regular response or a Server-Sent-Events stream. Locorum's tools
// are short request/response so we ship the simpler subset: POST a
// single JSON object, get a single JSON object back. A future
// revision can add SSE for tool calls that stream output (e.g. a
// long wp-cli run).
//
// Security: the listener defaults to 127.0.0.1, refuses 0.0.0.0
// without an explicit --bind flag, and requires a per-process bearer
// token (token.go). Filesystem perms on the token file (0600) limit
// the attack surface to other processes running as the same user.

// HTTPServer is the HTTP frontend for an existing daemon client. One
// goroutine handles the listen + accept loop; per-request dispatch is
// stateless so the http.Handler is goroutine-safe by construction.
type HTTPServer struct {
	addr   string
	token  string
	mux    *http.ServeMux
	core   *Server // wraps the same dispatch logic as stdio mode
	logger *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	srv      *http.Server
	stopped  bool
}

// HTTPOptions configures the HTTP server. Bind is the listen address
// (e.g. "127.0.0.1:2484"); Token is the bearer secret clients send in
// `Authorization: Bearer <token>`.
type HTTPOptions struct {
	Bind   string
	Token  string
	Server *Server // typically constructed via NewServer with stdio I/O wired to nil
	Logger *slog.Logger
}

// NewHTTPServer wires an HTTP listener around the existing dispatch
// engine. The caller must have already initialised opts.Server with a
// daemon Client; we don't second-guess the trust profile.
//
// Refuses non-loopback binds unless the caller explicitly asks for
// one by passing a pre-resolved IP — we only check the literal Bind
// string against a safe-list. The CLI shows a clear error before
// reaching this point so the message reads naturally.
func NewHTTPServer(opts HTTPOptions) (*HTTPServer, error) {
	if opts.Server == nil {
		return nil, errors.New("http mcp: server is required")
	}
	if opts.Token == "" {
		return nil, errors.New("http mcp: token is required")
	}
	if err := validateBind(opts.Bind); err != nil {
		return nil, err
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := &HTTPServer{
		addr:   opts.Bind,
		token:  opts.Token,
		mux:    http.NewServeMux(),
		core:   opts.Server,
		logger: logger.With("subsys", "mcp.http"),
	}
	h.mux.HandleFunc("/mcp", h.handleMCP)
	h.mux.HandleFunc("/healthz", h.handleHealth)
	return h, nil
}

// Serve binds the listener and serves requests until ctx cancels or
// Shutdown is called.
func (h *HTTPServer) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", h.addr, err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Allow the user's prompt to span multiple sentences; 1 MiB
		// covers typical tool calls without giving an attacker free
		// memory.
		MaxHeaderBytes: 1 << 20,
	}
	h.mu.Lock()
	h.listener = ln
	h.srv = srv
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		// ctx is already cancelled here; strip cancellation so the 5s
		// shutdown deadline still applies.
		_ = h.Shutdown(context.WithoutCancel(ctx))
	}()
	h.logger.Info("http mcp listening", "addr", h.actualAddr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// actualAddr returns the bound address as the OS reports it (so a
// :0 port shows up as a real number). Falls back to the requested
// addr when the listener is nil. Holds h.mu so the read of h.listener
// is data-race-safe against Serve's write.
func (h *HTTPServer) actualAddr() string {
	h.mu.Lock()
	ln := h.listener
	h.mu.Unlock()
	if ln == nil {
		return h.addr
	}
	return ln.Addr().String()
}

// Addr returns the bound listen address. Useful in tests that pass
// :0 to pick a free port.
func (h *HTTPServer) Addr() string { return h.actualAddr() }

// Shutdown stops accepting new connections and waits up to 5s for
// in-flight requests to finish. The supplied parent context contributes
// values and tracing; cancellation is layered on top of the 5s deadline
// (whichever fires first wins).
func (h *HTTPServer) Shutdown(parent context.Context) error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	h.stopped = true
	srv := h.srv
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// ServeHTTP is the http.Handler entry point — delegates to the mux.
// Defined explicitly so the type satisfies http.Handler with a
// custom error / log path.
func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleMCP is the JSON-RPC endpoint. Accepts POST /mcp with one
// JSON object in the body, dispatches via the same engine as stdio,
// and writes the response as a single JSON object. Notifications
// (id missing) return 204 No Content.
func (h *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if !h.checkAuth(r) {
		// 401 with a generic message — no hint about why so a probing
		// attacker can't tell whether the token was wrong or absent.
		w.Header().Set("WWW-Authenticate", `Bearer realm="locorum-mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 1 MiB body cap: tool calls don't exceed a few KB in practice. The
	// limit is +1 over the cap so we can detect "client sent exactly the
	// limit" (allowed) vs. "client sent more, we truncated" (rejected).
	const maxBodyBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(body) > maxBodyBytes {
		http.Error(w,
			fmt.Sprintf("request body exceeds %d bytes — chunk your tool call", maxBodyBytes),
			http.StatusRequestEntityTooLarge)
		return
	}
	r.Body.Close()

	// Reuse the stdio Server's dispatch by passing the body through
	// its in/out streams. We construct a one-shot pair: the request
	// goes in, the response comes out, then we tear down.
	var (
		in  = bytes.NewReader(append(body, '\n'))
		out bytes.Buffer
	)
	once := h.core.cloneForOneShot(in, &out)
	if err := once.Serve(r.Context()); err != nil {
		h.logger.Warn("dispatch failed", "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if out.Len() == 0 {
		// Notification — no response body per JSON-RPC spec.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Strip trailing newline json.Encoder added — most HTTP clients
	// don't care, but unit tests are easier without it.
	resp := bytes.TrimRight(out.Bytes(), "\n")
	_, _ = w.Write(resp)
}

// handleHealth is a tiny endpoint MCP clients can hit to verify the
// server is up before sending a real request. Returns 200 with a
// short JSON body. Authentication is NOT required — we surface no
// secrets and the bind is loopback by default.
func (h *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok","name":"locorum"}`)
}

// checkAuth pulls the Authorization header and constant-time-compares
// against the configured token. Header parsing is intentionally
// strict — we accept only "Bearer <token>" with no whitespace
// tolerance beyond a single space, so a malformed header is a hint
// the client is misconfigured rather than an attempt to bypass us.
func (h *HTTPServer) checkAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	got := auth[len(prefix):]
	return CompareTokens(got, h.token)
}

// validateBind enforces the localhost-default rule. We only allow:
//
//   - empty (caller will let net.Listen pick) — refused so the user
//     always sees an explicit address in logs
//   - 127.0.0.1:PORT or [::1]:PORT — explicit loopback
//   - localhost:PORT — also loopback after DNS resolution, but reject
//     because hostnames are inherently ambiguous (a hostile DNS could
//     point at 0.0.0.0); require the literal IP for clarity
//   - any other IPv4/IPv6 address — rejected with a clear message so
//     a user typing `--bind :2484` can't accidentally serve to the
//     network. They can override via `--bind 0.0.0.0:2484` in code,
//     but the CLI gates that path behind a separate flag.
func validateBind(bind string) error {
	if bind == "" {
		return errors.New("bind address is required (e.g. 127.0.0.1:2484)")
	}
	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return fmt.Errorf("invalid bind %q: %w", bind, err)
	}
	if port == "" {
		return fmt.Errorf("invalid bind %q: missing port", bind)
	}
	switch host {
	case "127.0.0.1", "::1":
		return nil
	case "":
		return fmt.Errorf("bind %q omits host; use 127.0.0.1:%s explicitly", bind, port)
	}
	// Anything else: caller must opt in by passing the literal
	// address. This is intentionally inflexible — the right answer
	// for "expose to my LAN" is "use a tunnel" or "wire your own
	// reverse proxy."
	return fmt.Errorf("bind %q is not a loopback address; use 127.0.0.1:%s", bind, port)
}

// cloneForOneShot returns a Server pointing at fresh in/out streams,
// sharing the same daemon client + profile + scope. Used by the HTTP
// handler so concurrent requests don't share scanner state.
//
// The clone is NOT thread-safe with itself — caller dispatches one
// frame and discards. New per-request because the underlying
// bufio.Scanner cannot be reused after EOF.
//
// We construct the clone field-by-field rather than by struct-copy:
// `*s` would copy the inner sync.Mutex and tip vet's copylocks check.
func (s *Server) cloneForOneShot(in io.Reader, out io.Writer) *Server {
	return &Server{
		in:      in,
		out:     out,
		client:  s.client,
		logger:  s.logger,
		scope:   s.scope,
		profile: s.profile,
		version: s.version,
	}
}

// SetCoreInOut lets the HTTP harness swap the underlying server's
// streams in tests. Unused outside the test binary; defined here so
// the test file doesn't need to reach into unexported fields.
func (s *Server) SetCoreInOut(in io.Reader, out io.Writer) {
	s.in = in
	s.out = out
}
