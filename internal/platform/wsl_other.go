//go:build !linux

package platform

import "context"

// detectWSL is the non-Linux stub. The plan-of-record is "WSL only matters
// when GOOS=linux", so we return the zero value unconditionally. The
// Windows-side "I can talk to WSL via wsl.exe" check stays in
// internal/utils.HasWSL — see CROSS-PLATFORM.md note v2 #4.
func detectWSL(_ context.Context) WSLInfo {
	return WSLInfo{}
}
