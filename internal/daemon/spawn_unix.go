//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// configureDetached makes the spawned daemon process survive its
// parent's exit. Setsid creates a new session so the child becomes its
// own process group leader and is no longer attached to the controlling
// terminal of the CLI that spawned it.
func configureDetached(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// nullDevice opens /dev/null in append mode for use as the daemon's
// stdout/stderr when the canonical log path is unwritable.
func nullDevice() *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	return f
}
