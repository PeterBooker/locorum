//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// configureDetached spawns the daemon detached so a CLI exit does not
// kill it. CreateNoWindow hides the console window the new process
// would otherwise inherit when launched from a GUI Locorum binary.
func configureDetached(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags = syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000 // CREATE_NO_WINDOW
}

// nullDevice opens NUL for the daemon's stdout/stderr fallback.
func nullDevice() *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	return f
}
