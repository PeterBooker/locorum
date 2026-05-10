package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/sites"
)

// caServerLifetime caps how long the pairing HTTP server stays up. Ten
// minutes is enough for a user to walk over to the device, scan, and
// install — and short enough to limit the window in which an attacker
// on the same Wi-Fi could probe the endpoint. The server also auto-
// shuts down after a single successful download.
const caServerLifetime = 10 * time.Minute

// caPairingServer is the short-lived HTTP server that exposes the
// mkcert root CA so a phone or tablet on the same Wi-Fi can install
// the trust anchor with a single QR-scan flow.
//
// Lifecycle:
//   - newCAPairingServer locates the root CA (via the SiteManager-
//     reachable TLS provider) and binds a listener on the host's LAN
//     IP at a random ephemeral port.
//   - Once the listener is up, a goroutine starts serving and arms
//     two shutdown timers: one tied to caServerLifetime and one armed
//     after the first successful 2xx GET.
//   - Stop is idempotent and safe to call from any goroutine.
//   - Done returns a channel closed when the server has exited (timer
//     fired, single-shot tripped, or Stop called). Callers use this to
//     clear UI state.
type caPairingServer struct {
	url    string
	server *http.Server
	done   chan struct{}

	stopOnce sync.Once
}

// newCAPairingServer locates the root CA, opens a listener bound to ip,
// and starts serving. Returns an error if:
//   - the SiteManager cannot satisfy a tls.Provider (no router yet) or
//     the provider does not know where the root CA file lives;
//   - the root CA file is missing or unreadable;
//   - the listener cannot bind on the chosen IP.
func newCAPairingServer(ctx context.Context, sm *sites.SiteManager, ip net.IP) (*caPairingServer, error) {
	if sm == nil {
		return nil, errors.New("nil SiteManager")
	}
	caPath, err := sm.RootCAPath(ctx)
	if err != nil {
		return nil, fmt.Errorf("locate root CA: %w", err)
	}
	if _, err := os.Stat(caPath); err != nil {
		return nil, fmt.Errorf("read root CA: %w", err)
	}

	addr := net.JoinHostPort(ip.String(), "0")
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("listener address is not TCP: %T", ln.Addr())
	}
	url := "http://" + ip.String() + ":" + strconv.Itoa(tcpAddr.Port) + "/rootCA.pem"

	srv := &caPairingServer{
		url:  url,
		done: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rootCA.pem", srv.serveCA(caPath))
	mux.HandleFunc("/", srv.notFound)

	srv.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		_ = srv.server.Serve(ln)
		close(srv.done)
	}()

	// Lifetime watchdog. Stop is idempotent. The shutdown context Stop
	// builds is intentionally detached: a panel close or site switch
	// must not cancel the 2-second grace mid-flush.
	go func() { //nolint:contextcheck // detached shutdown grace by design
		t := time.NewTimer(caServerLifetime)
		defer t.Stop()
		select {
		case <-t.C:
			srv.Stop()
		case <-srv.done:
		}
	}()

	return srv, nil
}

func (s *caPairingServer) URL() string           { return s.url }
func (s *caPairingServer) Done() <-chan struct{} { return s.done }

// Stop gracefully shuts the server down. Idempotent — safe to call
// from multiple goroutines.
func (s *caPairingServer) Stop() {
	s.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	})
}

// serveCA returns a handler that streams the root CA file with the
// content-type Android needs to recognise it as an installable cert
// payload. iOS recognises the .pem content directly without a special
// MIME type, so the same handler works for both. After the first
// successful 2xx response, a shutdown is queued: at most one device
// pairs per server boot.
func (s *caPairingServer) serveCA(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, "root CA unavailable", http.StatusNotFound)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			http.Error(w, "root CA unavailable", http.StatusNotFound)
			return
		}

		// Android: application/x-x509-ca-cert prompts the install flow.
		// iOS: identifies the payload from the leading PEM bytes; the
		// same Content-Type is harmless. Some iOS versions prefer
		// application/pkix-cert; the dual-platform answer is to ship
		// the .pem under the Android-recognised type and rely on iOS
		// content sniffing.
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="rootCA.pem"`)
		w.Header().Set("Cache-Control", "no-store")

		http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)

		// Auto-shutdown after the first successful download. Run async
		// so the response writer flushes cleanly first; HTTP/1.1
		// keep-alive does not delay this since the underlying conn is
		// closed during Shutdown. The shutdown context is intentionally
		// detached — using r.Context() would cancel the grace as soon
		// as the response is written, defeating Shutdown's purpose.
		go s.Stop() //nolint:contextcheck // detached shutdown grace by design
	}
}

func (s *caPairingServer) notFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "not found — request /rootCA.pem", http.StatusNotFound)
}
