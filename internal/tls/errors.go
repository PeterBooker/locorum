package tls

import "errors"

// ErrMkcertMissing is returned by Issue / CARoot / InstallCA when the
// mkcert binary is not installed on the host. The UI branches on
// errors.Is to render a banner with a "Show me how" action that opens
// the install instructions URL.
//
// Distinct from "mkcert is installed but the local CA isn't trusted yet"
// — that case stays a regular error. The sentinel is only attached when
// the binary itself is unreachable.
var ErrMkcertMissing = errors.New("mkcert binary not installed")
