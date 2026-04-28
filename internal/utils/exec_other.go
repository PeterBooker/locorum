//go:build !windows

package utils

import "os/exec"

// HideConsole is a no-op outside Windows. See exec_windows.go for the
// Windows implementation that suppresses the transient console window.
func HideConsole(*exec.Cmd) {}
