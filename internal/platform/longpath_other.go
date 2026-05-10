//go:build !windows

package platform

// readLongPathsEnabled is the non-Windows stub. The MAX_PATH limit is a
// Windows-only concern; on Linux/macOS we report "off" and the IsLongPath
// check (which is also gated by runtime.GOOS) renders the field unused.
func readLongPathsEnabled() bool {
	return false
}
