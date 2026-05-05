//go:build linux

package platform

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// detectWSL applies the multi-signal detection described in LEARNINGS §6.1.
// A host is in WSL if any of these are true:
//
//   - WSL_INTEROP env var set (the canonical marker; survives sub-shells)
//   - WSL_DISTRO_NAME env var set (legacy fallback)
//   - /proc/version contains "microsoft" or "wsl" (case-insensitive)
//   - /proc/sys/kernel/osrelease ends with "-WSL2" or "-microsoft-standard"
//
// Any single signal is sufficient. The function then attempts to populate
// .NetworkingMode by shelling out to `wslinfo`, bounded by the caller's
// context (typically WSLInfoTimeout).
func detectWSL(ctx context.Context) WSLInfo {
	out := WSLInfo{
		Distro: os.Getenv("WSL_DISTRO_NAME"),
	}

	if _, ok := os.LookupEnv("WSL_INTEROP"); ok {
		out.Active = true
	}
	if out.Distro != "" {
		out.Active = true
	}

	if data, err := os.ReadFile("/proc/version"); err == nil {
		lower := bytes.ToLower(data)
		if bytes.Contains(lower, []byte("microsoft")) || bytes.Contains(lower, []byte("wsl")) {
			out.Active = true
		}
	}

	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		out.KernelRelease = strings.TrimSpace(string(release))
		lower := strings.ToLower(out.KernelRelease)
		if strings.HasSuffix(lower, "-wsl2") || strings.HasSuffix(lower, "-microsoft-standard") {
			out.Active = true
		}
	}

	if out.Active {
		out.NetworkingMode = readWSLNetworkingMode(ctx)
	}

	return out
}

// readWSLNetworkingMode invokes `wslinfo --networking-mode` with the
// caller's context as a deadline. The output is one of "nat", "mirrored",
// or "virtioproxy" depending on the host's WSL config; on older WSL
// installs `wslinfo` does not exist and we return "".
//
// Errors are not surfaced — the value is informational. We do log at
// debug level so a future oncall can tell why the field is empty.
func readWSLNetworkingMode(ctx context.Context) string {
	bin, err := exec.LookPath("wslinfo")
	if err != nil {
		// Older WSL releases predate wslinfo. Not an error, just no
		// data — return silently.
		return ""
	}
	cmd := exec.CommandContext(ctx, bin, "--networking-mode")
	stdout, err := cmd.Output()
	if err != nil {
		// Don't pollute logs on a routine `command not found`-shaped
		// failure (the CommandContext exec didn't reach the binary).
		// For real failures (timeout, bad output) record one debug
		// line so platform-detection problems are diagnosable.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			slog.Debug("platform: wslinfo failed", "err", err.Error())
		}
		return ""
	}
	mode := strings.TrimSpace(string(stdout))
	switch mode {
	case "nat", "mirrored", "virtioproxy":
		return mode
	default:
		// Some `wslinfo` builds emit additional whitespace/headers.
		// Take the first non-empty token.
		fields := strings.Fields(mode)
		if len(fields) == 0 {
			return ""
		}
		return fields[0]
	}
}
