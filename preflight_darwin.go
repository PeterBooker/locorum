//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// runPreflight is the very first thing main() does on darwin: it traps the
// case where someone has installed the amd64 build of Locorum on an Apple
// Silicon Mac. Rosetta translates the binary, and Docker SDK calls
// subsequently fail with cryptic messages about platform mismatch.
//
// We hard-fail here — the user can't usefully recover from inside a
// translated process. Showing a native modal (via osascript so we don't
// have to link AppKit and pull cgo into preflight) gives the user a clear
// "reinstall the arm64 build" path; falling through to stderr keeps the
// command-line case usable.
//
// Exit code 78 is sysexits(3) EX_CONFIG — a configuration error the user
// must fix before retrying.
func runPreflight() {
	// Only the amd64 binary can be Rosetta-translated. arm64 binaries
	// always run native; intel Macs report the sysctl as missing.
	if runtime.GOARCH != "amd64" {
		return
	}
	if !readProcTranslated() {
		return
	}

	const exitConfig = 78
	msg := "Locorum: this is the Intel (amd64) build, but it is being translated by Rosetta on an Apple Silicon Mac.\n" +
		"Docker performance and container compatibility are unreliable in this configuration.\n\n" +
		"Please reinstall the arm64 (Apple Silicon) build."

	// stderr always — even if the modal works, the CLI user wants to see why.
	_, _ = fmt.Fprintln(os.Stderr, msg)

	// Best-effort native modal. Failure (osascript missing, sandbox
	// blocking, anything) is intentionally swallowed — we already have
	// a stderr message and we'd rather exit cleanly than panic on a
	// platform-detection helper.
	showRosettaModal(msg)

	os.Exit(exitConfig)
}

// readProcTranslated calls /usr/sbin/sysctl directly so we don't pull in
// cgo or unsafe SDK constants. The sysctl returns "1" when the running
// process is Rosetta-translated.
//
// Bound by a 1-second context: if sysctl hangs (it shouldn't, but defence
// in depth) we treat it as "not translated" and proceed. Worse outcome of
// a false negative: the regular Rosetta health check fires after Gio loads.
func readProcTranslated() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "sysctl.proc_translated")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}

// showRosettaModal displays a native macOS alert via osascript. We pin the
// path to /usr/bin/osascript so a $PATH override can't substitute a
// malicious binary. The argument is a constant string — user input never
// reaches it — so AppleScript injection is not a concern.
//
// 5-second timeout: the user only needs to see "this won't work"; the
// process is exiting either way.
func showRosettaModal(message string) {
	// AppleScript treats double quotes in the body specially; we don't
	// inject any user input into the message, so a constant escape pass
	// is enough.
	escaped := strings.ReplaceAll(message, `"`, `\"`)
	script := `display alert "Locorum can't run under Rosetta" message "` + escaped + `" as critical`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script)
	_ = cmd.Run()
}
