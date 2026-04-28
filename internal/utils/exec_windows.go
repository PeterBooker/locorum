//go:build windows

package utils

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag. The
// Go syscall package does not expose it as a named constant on every
// platform, so we hard-code the documented value. See:
// https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
const createNoWindow = 0x08000000

// HideConsole suppresses the transient console window Windows would
// otherwise allocate when a GUI-subsystem process spawns a console-
// subsystem child. No-op on non-Windows platforms.
func HideConsole(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
