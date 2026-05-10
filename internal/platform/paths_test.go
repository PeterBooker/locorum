package platform

import (
	"runtime"
	"strings"
	"testing"
)

func TestDockerPathSlashesAreNormalised(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/home/peter/locorum", "/home/peter/locorum"},
		{`C:\Users\Peter\locorum`, "C:/Users/Peter/locorum"},
		{`C:\Users\Peter/mixed\separators`, "C:/Users/Peter/mixed/separators"},
		{"already/slash/form", "already/slash/form"},
	}
	for _, c := range cases {
		got := DockerPath(c.in)
		if got != c.want {
			t.Errorf("DockerPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsLongPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		// IsLongPath is short-circuited to false on non-Windows. Confirm.
		if IsLongPath(strings.Repeat("a", 500)) {
			t.Error("IsLongPath should return false on non-windows hosts")
		}
		return
	}
	// On Windows, the threshold is WindowsMaxPath - 1 - WPMaxPluginPathSuffix.
	threshold := WindowsMaxPath - 1 - WPMaxPluginPathSuffix
	short := strings.Repeat("a", threshold)
	long := strings.Repeat("a", threshold+1)
	if IsLongPath(short) {
		t.Errorf("path of length %d should NOT be flagged long", len(short))
	}
	if !IsLongPath(long) {
		t.Errorf("path of length %d SHOULD be flagged long", len(long))
	}
	if IsLongPath("") {
		t.Error("empty path should not be flagged long")
	}
}

func TestIsMntDrvFsPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
		why  string
	}{
		{"/mnt/c/Users/foo", true, "drive c"},
		{"/mnt/d/projects/site", true, "drive d"},
		{"/mnt/C/Users/foo", true, "uppercase drive letter"},
		{"/mnt/c", true, "drive root, no trailing slash"},
		{"/home/peter/locorum", false, "linux home"},
		{"/mnt/drive/foo", false, "multi-letter pseudo-drive"},
		{"/mnt/", false, "no drive letter"},
		{"/mnt", false, "no trailing path at all"},
		{`C:\Users\Peter`, false, "windows-form path is not a /mnt/ path"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		got := isMntDrvFsPath(c.in)
		if got != c.want {
			t.Errorf("isMntDrvFsPath(%q) = %v, want %v (%s)", c.in, got, c.want, c.why)
		}
	}
}

// TestLongPathBlocking covers the registry-aware severity decision used
// by sites.ValidateSitePath: a path that breaches MAX_PATH only blocks
// when the OS does *not* have LongPathsEnabled set.
func TestLongPathBlocking(t *testing.T) {
	if runtime.GOOS != "windows" {
		// IsLongPath is short-circuited to false on non-Windows; assert
		// LongPathBlocking inherits that behaviour and never blocks on
		// Linux/macOS regardless of the registry shape.
		long := strings.Repeat("a", 500)
		off := &Info{OS: "linux", LongPathsEnabled: false}
		if LongPathBlocking(long, off) {
			t.Errorf("LongPathBlocking must return false on non-Windows hosts")
		}
		return
	}
	threshold := WindowsMaxPath - 1 - WPMaxPluginPathSuffix
	long := strings.Repeat("a", threshold+1)
	short := strings.Repeat("a", threshold)

	off := &Info{OS: "windows", LongPathsEnabled: false}
	on := &Info{OS: "windows", LongPathsEnabled: true}

	if !LongPathBlocking(long, off) {
		t.Error("long path on Windows with LongPathsEnabled=false MUST block")
	}
	if LongPathBlocking(long, on) {
		t.Error("long path on Windows with LongPathsEnabled=true must not block (OS handles it)")
	}
	if LongPathBlocking(short, off) {
		t.Error("short path must not block regardless of registry state")
	}
	if LongPathBlocking("", off) {
		t.Error("empty path must not block")
	}
	if LongPathBlocking(long, nil) {
		t.Error("nil info must not block")
	}
}

// TestIsMntCRespectsWSLActive confirms the public IsMntC short-circuits on
// non-WSL hosts even when the path *would* match. Test installs a fake
// platform.Info via NewForTest so we don't depend on the build host.
func TestIsMntCRespectsWSLActive(t *testing.T) {
	notWSL := &Info{OS: "linux", Arch: "amd64"} // WSL.Active default false
	restore := NewForTest(notWSL)
	defer restore()

	if IsMntC("/mnt/c/Users/foo") {
		t.Error("IsMntC must return false when WSL.Active is false")
	}

	wsl := &Info{OS: "linux", Arch: "amd64", WSL: WSLInfo{Active: true}}
	NewForTest(wsl) // this restore is overwritten by the outer defer

	if !IsMntC("/mnt/c/Users/foo") {
		t.Error("IsMntC must return true on a /mnt/c path with WSL active")
	}
	if IsMntC("/home/peter/locorum") {
		t.Error("IsMntC must return false for non-/mnt path")
	}
}
