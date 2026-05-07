package router

import "errors"

// ErrPortInUse is returned by Router.EnsureRunning implementations when
// the configured HTTP or HTTPS host port is already bound by something
// other than the router itself. Defined on the interface package so UI
// code can branch with errors.Is without importing a specific routing
// engine implementation.
var ErrPortInUse = errors.New("router host port already in use")
