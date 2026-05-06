package daemon

import (
	"net"
	"time"
)

// Listener is the OS-specific listening socket the daemon accepts JSON-
// RPC connections on. transport_unix.go and transport_windows.go each
// provide a concrete constructor.
type Listener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() string
}

// dialTimeout is the upper bound on how long a CLI client waits for the
// daemon's socket to become available after auto-spawn. The daemon
// finishes binding the socket as part of its normal startup, before
// app.Initialize touches Docker, so this only needs to be long enough
// to cover process exec + the few syscalls before Listen returns. Two
// seconds is generous; the goroutine is bounded by ctx anyway.
const dialTimeout = 2 * time.Second
