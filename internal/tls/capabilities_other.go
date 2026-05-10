//go:build !windows

package tls

// detectFirefoxOnWindows is the non-Windows stub. The Firefox-on-other-
// OS trust paths are handled by mkcert's own NSS branch (Linux + macOS)
// without further user action, so there's no Note to surface.
func detectFirefoxOnWindows() bool {
	return false
}
