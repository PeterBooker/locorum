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
