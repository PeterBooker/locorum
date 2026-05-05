package platform

import (
	"context"
	"runtime"
	"testing"
)

// TestInitIdempotent asserts repeated Init calls return the same pointer.
// The very first call within a test binary may be racing with other tests
// — guard via NewForTest so we don't pollute global state.
func TestInitIdempotent(t *testing.T) {
	restore := NewForTest(nil)
	defer restore()

	a := Init(context.Background())
	b := Init(context.Background())
	if a != b {
		t.Fatalf("Init must return the same pointer on repeated calls; got %p then %p", a, b)
	}
}

// TestGetPanicsBeforeInit verifies the safety net for a wiring bug.
func TestGetPanicsBeforeInit(t *testing.T) {
	restore := NewForTest(nil)
	defer restore()

	defer func() {
		if recover() == nil {
			t.Fatal("Get must panic when Init has not been called")
		}
	}()
	_ = Get()
}

// TestNewForTestRestoresPrevious confirms the test override is reversible
// so parallel-test files can swap and revert without leaking state.
func TestNewForTestRestoresPrevious(t *testing.T) {
	restore := NewForTest(&Info{OS: "fake-original"})
	defer restore()

	mid := NewForTest(&Info{OS: "fake-replacement"})
	if Get().OS != "fake-replacement" {
		t.Fatalf("expected mid-override; got %+v", Get())
	}
	mid()
	if Get().OS != "fake-original" {
		t.Fatalf("expected restore to original; got %+v", Get())
	}
}

// TestDetectFillsMandatoryFields runs detect against the real host. We
// can't assert specific values, but the contract is "OS/Arch/UID/GID/
// HomeDir/Username always populated" — it's worth catching a regression
// where one of those silently goes empty.
func TestDetectFillsMandatoryFields(t *testing.T) {
	info := detect(context.Background())
	if info.OS == "" {
		t.Error("OS must be populated")
	}
	if info.Arch == "" {
		t.Error("Arch must be populated")
	}
	if info.OS != runtime.GOOS {
		t.Errorf("OS=%q does not match runtime.GOOS=%q", info.OS, runtime.GOOS)
	}
	if info.Arch != runtime.GOARCH {
		t.Errorf("Arch=%q does not match runtime.GOARCH=%q", info.Arch, runtime.GOARCH)
	}
	if info.Username == "" {
		t.Error("Username must be populated (sanitiser fallback should never empty it)")
	}
	if info.HomeDir == "" {
		// HomeDir can legitimately be empty if the test runner has no
		// HOME env var (rare). Not a test failure — record only.
		t.Logf("HomeDir empty; runner has no HOME? OS=%s", info.OS)
	}
}

// TestNewForTestNilThenInitDetects ensures clearing the cache to nil
// (the documented restore path) lets a fresh Init populate it again.
func TestNewForTestNilThenInitDetects(t *testing.T) {
	restore := NewForTest(nil)
	defer restore()

	if IsInitialized() {
		t.Fatal("IsInitialized should be false after NewForTest(nil)")
	}
	got := Init(context.Background())
	if got == nil {
		t.Fatal("Init returned nil")
	}
	if !IsInitialized() {
		t.Fatal("IsInitialized should be true after Init")
	}
}
