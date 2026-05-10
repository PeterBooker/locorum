package sites

import (
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/platform"
)

func TestValidateSitePathEmpty(t *testing.T) {
	if got := ValidateSitePath("", &platform.Info{OS: "linux"}); len(got) != 0 {
		t.Errorf("empty path should have no notes, got %v", got)
	}
}

func TestValidateSitePathNilInfo(t *testing.T) {
	if got := ValidateSitePath("/home/peter/sites/foo", nil); len(got) != 0 {
		t.Errorf("nil info should have no notes, got %v", got)
	}
}

func TestValidateSitePathWSLMntC(t *testing.T) {
	wsl := &platform.Info{OS: "linux", WSL: platform.WSLInfo{Active: true}}
	restore := platform.NewForTest(wsl)
	defer restore()

	got := ValidateSitePath("/mnt/c/Users/foo/sites/a", wsl)
	if len(got) != 1 {
		t.Fatalf("expected 1 note, got %d", len(got))
	}
	if got[0].Severity != PathSeverityWarn {
		t.Errorf("WSL mnt/c note should be warn, got %v", got[0].Severity)
	}
	if !strings.Contains(strings.ToLower(got[0].Title), "slow") {
		t.Errorf("expected 'slow' in title, got %q", got[0].Title)
	}
}

func TestValidateSitePathWSLNativeIsClean(t *testing.T) {
	wsl := &platform.Info{OS: "linux", WSL: platform.WSLInfo{Active: true}}
	restore := platform.NewForTest(wsl)
	defer restore()

	got := ValidateSitePath("/home/peter/sites/a", wsl)
	if len(got) != 0 {
		t.Errorf("native WSL path should produce no notes, got %v", got)
	}
}

func TestValidateSitePathLinuxIsAlwaysClean(t *testing.T) {
	info := &platform.Info{OS: "linux"} // no WSL
	got := ValidateSitePath(strings.Repeat("a", 500), info)
	if len(got) != 0 {
		t.Errorf("linux + long path should produce no notes; long-path is windows-only; got %v", got)
	}
}

// TestValidateSitePathWindowsLongBlocksWhenRegistryOff covers the F12
// Plan §1: on a Windows host whose registry has LongPathsEnabled = 0
// (or unread), a long path must produce a [PathSeverityBlock] note so
// AddSite refuses and the UI gates the Create button. We construct the
// platform.Info directly rather than relying on the runtime.GOOS-gated
// IsLongPath: the Linux test runner can't observe runtime.GOOS=windows,
// so the long-path leg is skipped on non-Windows builds. The point of
// the test is to lock in the *severity* for cases that *do* trigger.
func TestValidateSitePathWindowsLongBlocksWhenRegistryOff(t *testing.T) {
	// IsLongPath is gated on runtime.GOOS=="windows", so this test is a
	// no-op on Linux. The non-Windows assertion lives in
	// TestValidateSitePathLinuxIsAlwaysClean above; here we only assert
	// that *if* the leg fires, severity matches the registry state.
	info := &platform.Info{OS: "windows", LongPathsEnabled: false}
	long := strings.Repeat("a", 200)
	notes := ValidateSitePath(long, info)
	if !platform.IsLongPath(long) {
		// Cross-platform: IsLongPath bails on non-Windows runtimes.
		// Skip the assertion rather than fail the test on Linux CI.
		if len(notes) != 0 {
			t.Fatalf("non-Windows runtime: expected no notes, got %d", len(notes))
		}
		t.Skip("IsLongPath is runtime.GOOS-gated; skipping severity check on non-Windows host")
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Severity != PathSeverityBlock {
		t.Errorf("LongPathsEnabled=false: expected blocking severity, got %v", notes[0].Severity)
	}
	if notes[0].Remediation == "" {
		t.Errorf("blocking note should set Remediation; got empty")
	}
}

// TestValidateSitePathWindowsLongWarnsWhenRegistryOn covers the
// downgrade leg: with LongPathsEnabled = 1 the OS handles the overflow,
// so we keep the note as a soft Warn rather than a Block.
func TestValidateSitePathWindowsLongWarnsWhenRegistryOn(t *testing.T) {
	info := &platform.Info{OS: "windows", LongPathsEnabled: true}
	long := strings.Repeat("a", 200)
	notes := ValidateSitePath(long, info)
	if !platform.IsLongPath(long) {
		t.Skip("IsLongPath is runtime.GOOS-gated; skipping severity check on non-Windows host")
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Severity != PathSeverityWarn {
		t.Errorf("LongPathsEnabled=true: expected warn severity, got %v", notes[0].Severity)
	}
}

// TestHasBlockingNote covers the helper used by the new-site modal and
// the SiteManager preflight.
func TestHasBlockingNote(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if HasBlockingNote(nil) {
			t.Errorf("nil notes should not be blocking")
		}
	})
	t.Run("only warns", func(t *testing.T) {
		notes := []PathNote{{Severity: PathSeverityWarn}, {Severity: PathSeverityWarn}}
		if HasBlockingNote(notes) {
			t.Errorf("only-warns should not be blocking")
		}
	})
	t.Run("contains blocker", func(t *testing.T) {
		notes := []PathNote{{Severity: PathSeverityWarn}, {Severity: PathSeverityBlock}}
		if !HasBlockingNote(notes) {
			t.Errorf("any blocker should be blocking")
		}
	})
}
