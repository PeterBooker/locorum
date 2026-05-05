package health

import (
	"context"
	"net"
	"strconv"
	"time"
)

// portProbeTimeout caps the dial step. We don't want a flaky network
// stack to hold a health-check goroutine for seconds; a busy port
// answers in well under 50ms locally, and a dead probe should fail fast.
const portProbeTimeout = 200 * time.Millisecond

// portInUse reports whether *something* is listening on 127.0.0.1:port.
// It does NOT bind the port — there is no TOCTOU window. A successful
// dial means a listener accepted the SYN and responded with SYN-ACK.
//
// We deliberately accept any TCP listener, not just HTTP. The caller
// (the port-conflict check) decides whether the listener is *ours* via
// a separate signal (engine.ContainerIsRunning) — see CROSS-PLATFORM.md
// note v2 #1.
func portInUse(ctx context.Context, port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	d := net.Dialer{Timeout: portProbeTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
