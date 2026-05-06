package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a goroutine-safe JSON-RPC 2.0 client speaking to a Locorum
// daemon over the IPC transport. Calls are matched by an integer "id"
// generated atomically per request; the demuxer goroutine routes each
// response to its waiting caller via a per-id channel.
type Client struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *Response
	closed  bool
	closeCh chan struct{}
}

// HelloOptions configures the client.hello handshake. Most clients
// declare PeerKind ("cli", "mcp", "gui-test") so the daemon can
// distinguish traffic in telemetry. Profile / MCPScope are MCP-only.
type HelloOptions struct {
	PeerKind string
	Profile  string
	MCPScope string
}

// Dial connects to the daemon socket / pipe and performs the
// client.hello handshake. ctx bounds the dial + handshake.
func DialClient(ctx context.Context, socket string, hello HelloOptions) (*Client, error) {
	conn, err := Dial(ctx, socket)
	if err != nil {
		return nil, err
	}
	cli := newClient(conn)
	go cli.readLoop()

	// Even an empty hello succeeds against a Full daemon: it just
	// stamps the conn with the defaults. We always send one so the
	// daemon knows where to route activity events for telemetry.
	helloParams := struct {
		PeerKind string `json:"peerKind,omitempty"`
		Profile  string `json:"profile,omitempty"`
		MCPScope string `json:"mcpScope,omitempty"`
	}{
		PeerKind: hello.PeerKind,
		Profile:  hello.Profile,
		MCPScope: hello.MCPScope,
	}
	var info serverInfo
	if err := cli.Call(ctx, "client.hello", helloParams, &info); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("client.hello: %w", err)
	}
	return cli, nil
}

func newClient(conn net.Conn) *Client {
	return &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		dec:     json.NewDecoder(bufio.NewReaderSize(conn, 64*1024)),
		pending: make(map[int64]chan *Response),
		closeCh: make(chan struct{}),
	}
}

// Call invokes method with params, blocks until the daemon responds (or
// ctx is done), and unmarshals the result into out (a pointer to a
// struct). Pass nil for params or out when neither is needed.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	idJSON, err := json.Marshal(id)
	if err != nil {
		return fmt.Errorf("encode id: %w", err)
	}

	var paramsRaw json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode params: %w", err)
		}
		paramsRaw = body
	}

	req := Request{
		JSONRPC: jsonRPCVersion,
		ID:      idJSON,
		Method:  method,
		Params:  paramsRaw,
	}

	respCh := make(chan *Response, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("client is closed")
	}
	c.pending[id] = respCh
	c.mu.Unlock()
	// Always remove the pending entry on exit so a cancelled context
	// doesn't leak a channel forever.
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeFrame(req); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closeCh:
		return errors.New("connection closed")
	case resp := <-respCh:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode result: %w", err)
			}
		}
		return nil
	}
}

// writeFrame serialises one request under the client mutex (the encoder
// shares a buffer with the underlying net.Conn writer).
func (c *Client) writeFrame(req Request) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("client is closed")
	}
	return c.enc.Encode(req)
}

// readLoop reads responses from the conn and routes each to its waiting
// Call via the pending map. Exits when the connection closes; failure
// to find a pending entry is logged-but-ignored (the caller may have
// timed out and dropped its channel).
func (c *Client) readLoop() {
	defer c.shutdown(nil)
	for {
		var resp Response
		if err := c.dec.Decode(&resp); err != nil {
			c.shutdown(err)
			return
		}
		// id is JSON-encoded as a number — unmarshal back to int64.
		// Defensive: malformed servers might send strings; we drop
		// them rather than panic.
		var id int64
		if len(resp.ID) > 0 {
			if err := json.Unmarshal(resp.ID, &id); err != nil {
				continue
			}
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		c.mu.Unlock()
		if !ok {
			continue
		}
		// respCh is buffered size-1 from Call; non-blocking send.
		select {
		case ch <- &resp:
		default:
		}
	}
}

// shutdown drains pending channels and marks the client closed.
// Idempotent. _err is recorded for diagnostics but otherwise unused —
// callers see a generic "connection closed" error from Call.
func (c *Client) shutdown(_ error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.closeCh)
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	_ = c.conn.Close()
}

// Close terminates the client connection. Safe to call multiple times;
// pending Calls return "connection closed".
func (c *Client) Close() error {
	c.shutdown(nil)
	return nil
}

// EnsureDaemon returns a connected client to the daemon, auto-spawning
// a headless daemon process if none is running. The auto-spawn path is
// the foundation of the "CLI just works" UX: a user types
// `locorum site list` on a fresh box and we transparently bring up the
// backend, run their query, and leave it running for next time.
//
// On Windows the auto-spawn uses Cmd.Process.Release so the parent CLI
// can exit without taking the daemon with it; on POSIX we set Setsid
// so the daemon survives the controlling terminal closing.
//
// homeDir, lockPath, socketPath: the canonical state-dir paths. Pass
// the same values the daemon process would compute.
//
// exePath: the absolute path to the locorum binary. Pass os.Executable
// in production; tests override with a mock binary that signals readiness
// the same way (binds the socket, holds it open).
//
// hello: handshake metadata, recorded in conn metadata.
//
// Auto-spawn is idempotent against races: two CLIs starting at once
// will both try to spawn; one Acquire wins the lock and the other dials
// in normally.
func EnsureDaemon(ctx context.Context, homeDir, exePath string, hello HelloOptions) (*Client, error) {
	socket := SocketPath(homeDir)

	// Fast path: a daemon is already up.
	if cli, err := DialClient(ctx, socket, hello); err == nil {
		return cli, nil
	} else if !errors.Is(err, ErrNoDaemon) {
		// A real error (perm denied, crashed daemon mid-handshake) —
		// bubble it up. Auto-spawn would just race with whatever's
		// holding the lock.
		return nil, err
	}

	if exePath == "" {
		// Without a binary path we can't spawn. Caller should have
		// resolved os.Executable at startup.
		return nil, ErrNoDaemon
	}

	if err := spawnDaemon(exePath, homeDir); err != nil {
		return nil, fmt.Errorf("spawn daemon: %w", err)
	}

	// Poll until the socket appears or ctx times out. The daemon
	// finishes binding the socket before app.Initialize touches
	// Docker, so this typically takes <100ms; we keep a 5s upper
	// bound for slow init.
	deadline := time.Now().Add(5 * time.Second)
	for {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		cli, err := DialClient(dialCtx, socket, hello)
		cancel()
		if err == nil {
			return cli, nil
		}
		if !errors.Is(err, ErrNoDaemon) && !isConnRefused(err) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("daemon did not bind socket within timeout: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// isConnRefused is the cross-platform "no listener on socket" check.
// Returned by Dial when the socket file exists but isn't accepting
// (race window during daemon startup).
func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	// We don't import syscall.ECONNREFUSED here to keep this file
	// platform-portable; the string match is good enough for our
	// "wait for daemon to bind" loop.
	msg := err.Error()
	for _, needle := range []string{"connection refused", "no such file", "cannot find the file"} {
		if containsFold(msg, needle) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	// ASCII-only fold is sufficient for the error strings we match.
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// daemonReadyEnv is the env var the auto-spawned daemon sees that tells
// it to come up headless (no Gio window). main.go reads this and skips
// the GUI bring-up.
const daemonReadyEnv = "LOCORUM_DAEMON_MODE"

// daemonReadyValue is the magic value of LOCORUM_DAEMON_MODE that
// signals "yes, you are an auto-spawned daemon."
const daemonReadyValue = "1"

// spawnDaemon execs the locorum binary with the daemon-mode env var
// set. The child detaches from the parent's stdout/stdin so a CLI exit
// doesn't kill the daemon. Logs route to ~/.locorum/state/daemon.log
// so the auto-spawn case is debuggable post-mortem.
func spawnDaemon(exePath, homeDir string) error {
	logPath := SocketPath(homeDir) + ".log" // sits next to socket; same 0600 perm scope
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		// Log file is optional. A locked filesystem shouldn't block
		// auto-spawn — fall back to /dev/null on POSIX.
		logFile = nullDevice()
	}
	cmd := exec.Command(exePath, "daemon", "--from-spawn")
	cmd.Env = append(os.Environ(), daemonReadyEnv+"="+daemonReadyValue)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureDetached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Don't Wait — the daemon outlives the CLI. Release so Go's
	// runtime cleans up the spawn-side state.
	return cmd.Process.Release()
}

// IsDaemonAutoSpawn returns true when the current process was started
// via spawnDaemon. main.go branches on this to skip Gio.
func IsDaemonAutoSpawn() bool {
	return os.Getenv(daemonReadyEnv) == daemonReadyValue
}

// FormatLockOwner returns a human-readable description of the current
// lock holder. Intended for CLI diagnostics ("daemon is already
// running, pid=12345 since 2026-05-06T12:00:00Z").
func FormatLockOwner(o Owner) string {
	if o.PID == 0 {
		return "unknown"
	}
	return "pid=" + strconv.Itoa(o.PID) + " started=" + o.StartedAt.Format(time.RFC3339)
}
