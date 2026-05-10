//go:build windows

package tls

import (
	"os"
	"path/filepath"
)

// detectFirefoxOnWindows checks the canonical install paths for a
// Firefox executable. We deliberately avoid the registry — registry
// reads from a Wayland/XWayland-shaped context have surprising failure
// modes, and the install paths are stable across every Firefox version
// shipped in the last decade.
//
// Returns false on any miss (including ENOENT for ProgramFiles).
func detectFirefoxOnWindows() bool {
	envKeys := []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"}
	for _, key := range envKeys {
		base := os.Getenv(key)
		if base == "" {
			continue
		}
		candidate := filepath.Join(base, "Mozilla Firefox", "firefox.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
