//go:build linux

package platform

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestDetectWSLEnvSignals exercises the env-var branch. We restore env vars
// after each subtest so we don't pollute later tests.
func TestDetectWSLEnvSignals(t *testing.T) {
	cases := []struct {
		envKey, envVal string
		wantActive     bool
	}{
		{"WSL_INTEROP", "/run/WSL/something", true},
		{"WSL_DISTRO_NAME", "Ubuntu-22.04", true},
	}
	for _, c := range cases {
		t.Run(c.envKey, func(t *testing.T) {
			t.Setenv(c.envKey, c.envVal)
			info := detectWSL(context.Background())
			if info.Active != c.wantActive {
				t.Errorf("Active=%v want %v (env %s=%s)", info.Active, c.wantActive, c.envKey, c.envVal)
			}
			if c.envKey == "WSL_DISTRO_NAME" && info.Distro != c.envVal {
				t.Errorf("Distro=%q want %q", info.Distro, c.envVal)
			}
		})
	}
}

// TestDetectWSLNoSignalsDefaultsToInactive confirms that on a system with
// none of the env signals and no /proc/version "microsoft", we return
// Active=false. We can't fully guarantee /proc/version doesn't say microsoft
// on the test host (CI may run inside WSL), so skip the assertion if it
// does. The intent of the test is the *negative* case — we don't want false
// positives from no signal.
func TestDetectWSLNoSignalsDefaultsToInactive(t *testing.T) {
	// Hide the env vars by ensuring they're unset for the duration.
	t.Setenv("WSL_INTEROP", "")
	t.Setenv("WSL_DISTRO_NAME", "")
	os.Unsetenv("WSL_INTEROP")
	os.Unsetenv("WSL_DISTRO_NAME")

	procVersion, _ := os.ReadFile("/proc/version")
	osRelease, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	procSaysWSL := bytesContainsCI(procVersion, "microsoft") ||
		bytesContainsCI(procVersion, "wsl") ||
		strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(osRelease))), "-wsl2") ||
		strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(osRelease))), "-microsoft-standard")

	info := detectWSL(context.Background())
	if !procSaysWSL && info.Active {
		t.Errorf("expected Active=false on host with no WSL signals; got %+v", info)
	}
}

func bytesContainsCI(haystack []byte, needle string) bool {
	return strings.Contains(strings.ToLower(string(haystack)), strings.ToLower(needle))
}
