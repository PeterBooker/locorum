package sites

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/platform"
)

// TestCheckPathBlockingPassesWhenPlatformUninit covers the safety
// fallback: if platform.Init has not been called (e.g. early-startup
// scripted callers, certain CLI sub-commands), the check is a no-op
// rather than a panic. Production callers always run after Init, so this
// is purely a defence in depth.
func TestCheckPathBlockingPassesWhenPlatformUninit(t *testing.T) {
	restore := platform.NewForTest(nil)
	defer restore()

	sm := &SiteManager{}
	if err := sm.checkPathBlocking("/whatever"); err != nil {
		t.Errorf("uninit platform should not block: %v", err)
	}
}

// TestCheckPathBlockingNoOpOnLinux confirms the check is silent for
// every Linux/macOS host even on absurd path lengths — the long-path
// limit is a Windows-only concern.
func TestCheckPathBlockingNoOpOnLinux(t *testing.T) {
	info := &platform.Info{OS: "linux", LongPathsEnabled: false}
	restore := platform.NewForTest(info)
	defer restore()

	sm := &SiteManager{}
	if err := sm.checkPathBlocking(strings.Repeat("a", 500)); err != nil {
		t.Errorf("linux host should not block long paths: %v", err)
	}
}

// TestCheckPathBlockingFailsOnWindowsWithoutLongPaths is the load-bearing
// case for F12: a long path on a Windows host without LongPathsEnabled
// produces ErrPathTooLong wrapping the validate.go remediation message.
// The IsLongPath gate is runtime.GOOS-bound, so on Linux test runners
// we skip the assertion side and only assert the success path above.
func TestCheckPathBlockingFailsOnWindowsWithoutLongPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("IsLongPath is runtime.GOOS-gated; full enforcement only observable on Windows")
	}
	info := &platform.Info{OS: "windows", LongPathsEnabled: false}
	restore := platform.NewForTest(info)
	defer restore()

	sm := &SiteManager{}
	long := strings.Repeat("a", 200)
	err := sm.checkPathBlocking(long)
	if err == nil {
		t.Fatal("expected ErrPathTooLong, got nil")
	}
	if !errors.Is(err, ErrPathTooLong) {
		t.Errorf("expected errors.Is(err, ErrPathTooLong) == true; err = %v", err)
	}
}

// TestCheckPathBlockingPassesWhenLongPathsEnabled ensures the registry
// opt-in downgrades the gate. Same runtime.GOOS guard as above.
func TestCheckPathBlockingPassesWhenLongPathsEnabled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("IsLongPath is runtime.GOOS-gated; full enforcement only observable on Windows")
	}
	info := &platform.Info{OS: "windows", LongPathsEnabled: true}
	restore := platform.NewForTest(info)
	defer restore()

	sm := &SiteManager{}
	long := strings.Repeat("a", 200)
	if err := sm.checkPathBlocking(long); err != nil {
		t.Errorf("LongPathsEnabled=true should not block long paths: %v", err)
	}
}
