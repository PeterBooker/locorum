package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/daemon"
)

// startHTTPServer spins up the HTTP MCP server on :0 (free port) so
// concurrent tests don't collide.
func startHTTPServer(t *testing.T, profile string) (*HTTPServer, string) {
	t.Helper()
	core := NewServer(Options{
		In:      strings.NewReader(""), // unused; HTTP swaps streams
		Out:     io.Discard,
		Profile: profile,
		Version: "test",
	})
	srv, err := NewHTTPServer(HTTPOptions{
		Bind:   "127.0.0.1:0",
		Token:  "secret-token-123",
		Server: core,
	})
	if err != nil {
		t.Fatalf("NewHTTPServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ready := make(chan string, 1)
	go func() {
		// Block briefly so Serve binds the listener; the test then
		// reads Addr.
		go func() {
			for i := 0; i < 50; i++ {
				if a := srv.Addr(); a != "" && !strings.HasSuffix(a, ":0") {
					ready <- a
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			ready <- ""
		}()
		_ = srv.Serve(ctx)
	}()
	addr := <-ready
	if addr == "" {
		t.Fatalf("server did not bind")
	}
	t.Cleanup(func() { _ = srv.Shutdown() })
	return srv, addr
}

func TestHTTP_ValidateBind_LoopbackOnly(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:2484": true,
		"[::1]:2484":     true,
		"0.0.0.0:2484":   false,
		"localhost:2484": false,
		":2484":          false,
		"":               false,
		"badform":        false,
	}
	for in, ok := range cases {
		t.Run(in, func(t *testing.T) {
			err := validateBind(in)
			if ok && err != nil {
				t.Fatalf("want allow %q, got %v", in, err)
			}
			if !ok && err == nil {
				t.Fatalf("want reject %q", in)
			}
		})
	}
}

func TestHTTP_AuthRequired(t *testing.T) {
	_, addr := startHTTPServer(t, daemon.ProfileFull)

	// No auth header → 401.
	resp, err := http.Post("http://"+addr+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got == "" {
		t.Fatalf("expected WWW-Authenticate header, got empty")
	}
}

func TestHTTP_WrongTokenRejected(t *testing.T) {
	_, addr := startHTTPServer(t, daemon.ProfileFull)

	req, _ := http.NewRequest("POST", "http://"+addr+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", resp.StatusCode)
	}
}

func TestHTTP_InitializeHandshake(t *testing.T) {
	_, addr := startHTTPServer(t, daemon.ProfileReadOnly)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req, _ := http.NewRequest("POST", "http://"+addr+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token-123")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: %d, body=%s", resp.StatusCode, buf)
	}
	respBody, _ := io.ReadAll(resp.Body)
	var parsed struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		t.Fatalf("parse response: %v body=%s", err, respBody)
	}
	if parsed.Result.ServerInfo.Name != "locorum" {
		t.Fatalf("server name: %q", parsed.Result.ServerInfo.Name)
	}
}

func TestHTTP_HealthzNoAuth(t *testing.T) {
	_, addr := startHTTPServer(t, daemon.ProfileFull)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("ok")) {
		t.Fatalf("healthz body: %s", body)
	}
}

func TestHTTP_PostOnly(t *testing.T) {
	_, addr := startHTTPServer(t, daemon.ProfileFull)
	resp, err := http.Get("http://" + addr + "/mcp")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status: %d", resp.StatusCode)
	}
}

func TestHTTP_TokenFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	tok, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	if tok == "" {
		t.Fatalf("empty token")
	}
	again, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if again != tok {
		t.Fatalf("token mismatch on re-load: %q vs %q", again, tok)
	}
	rotated, err := RotateToken(dir)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated == tok {
		t.Fatalf("rotate didn't change token")
	}
}
