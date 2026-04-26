package sites

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/hooks/fake"
	"github.com/PeterBooker/locorum/internal/types"
)

// minimalSiteManager builds a SiteManager with only the fields runHooks
// touches. The full struct needs Docker / storage / router; for hook-flow
// tests those dependencies stay nil.
func minimalSiteManager(runner hooks.Runner) *SiteManager {
	return &SiteManager{
		hooks:     runner,
		siteLocks: make(map[string]*sync.Mutex),
	}
}

func testHookSite() *types.Site {
	return &types.Site{
		ID: "s1", Slug: "demo", Domain: "demo.localhost",
		FilesDir: "/tmp", DBPassword: "p",
	}
}

func TestRunHooks_FiresAndForwardsToCallbacks(t *testing.T) {
	runner := fake.New()
	sm := minimalSiteManager(runner)

	var receivedSiteID string
	sm.OnHookAllDone = func(siteID string, _ hooks.Summary) {
		receivedSiteID = siteID
	}

	if err := sm.runHooks(context.Background(), hooks.PostStart, testHookSite()); err != nil {
		t.Fatalf("runHooks: %v", err)
	}
	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].Event != hooks.PostStart {
		t.Errorf("event = %q, want post-start", calls[0].Event)
	}
	if receivedSiteID != "s1" {
		t.Errorf("OnHookAllDone siteID = %q, want s1", receivedSiteID)
	}
}

func TestRunHooks_PropagatesError(t *testing.T) {
	runner := fake.New()
	runner.RunErr = context.Canceled
	sm := minimalSiteManager(runner)

	if err := sm.runHooks(context.Background(), hooks.PreStart, testHookSite()); err == nil {
		t.Error("expected runHooks to propagate runner error")
	}
}

func TestRunHooks_NoOpWithNilRunner(t *testing.T) {
	sm := minimalSiteManager(nil)
	if err := sm.runHooks(context.Background(), hooks.PostStart, testHookSite()); err != nil {
		t.Errorf("runHooks(nil runner) = %v, want nil", err)
	}
}

func TestRunHooks_NoOpWithNilSite(t *testing.T) {
	runner := fake.New()
	sm := minimalSiteManager(runner)
	if err := sm.runHooks(context.Background(), hooks.PostStart, nil); err != nil {
		t.Errorf("runHooks(nil site) = %v, want nil", err)
	}
	if len(runner.Calls()) != 0 {
		t.Errorf("runner called %d times, want 0", len(runner.Calls()))
	}
}

func TestSiteMutex_SerialisesPerSite(t *testing.T) {
	sm := minimalSiteManager(fake.New())

	// Two goroutines on the same site should run sequentially.
	muA := sm.siteMutex("a")
	muA.Lock()

	released := make(chan struct{})
	go func() {
		muA2 := sm.siteMutex("a")
		muA2.Lock()
		close(released)
		muA2.Unlock()
	}()

	select {
	case <-released:
		t.Fatal("second Lock returned before first Unlock — mutex not respected")
	case <-time.After(50 * time.Millisecond):
		// Good — second goroutine is still waiting.
	}

	muA.Unlock()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("second Lock never returned after first Unlock")
	}
}

func TestSiteMutex_DistinctSitesRunInParallel(t *testing.T) {
	sm := minimalSiteManager(fake.New())

	muA := sm.siteMutex("a")
	muA.Lock()
	defer muA.Unlock()

	released := make(chan struct{})
	go func() {
		muB := sm.siteMutex("b")
		muB.Lock()
		close(released)
		muB.Unlock()
	}()

	select {
	case <-released:
		// Good — different site lock acquired immediately.
	case <-time.After(time.Second):
		t.Fatal("different-site Lock should not block on another site's mutex")
	}
}

func TestSiteMutex_SameInstanceForSameID(t *testing.T) {
	sm := minimalSiteManager(fake.New())
	if sm.siteMutex("x") != sm.siteMutex("x") {
		t.Error("siteMutex returned different instances for the same id")
	}
}
