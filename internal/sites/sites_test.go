package sites

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/hooks/fake"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// minimalSiteManager builds a SiteManager with only the fields runHooks
// touches. The full struct needs Docker / storage / router; for hook-flow
// tests those dependencies stay nil.
func minimalSiteManager(runner hooks.Runner) *SiteManager {
	return &SiteManager{
		hooks: runner,
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
	a := sm.siteMutex("x")
	b := sm.siteMutex("x")
	if a != b {
		t.Error("siteMutex returned different instances for the same id")
	}
}

// spxTestSite returns a stopped site row backed by tmpDir as FilesDir.
func spxTestSite(tmpDir string) types.Site {
	return types.Site{
		ID: "spx-id", Name: "SPX", Slug: "spxsite",
		Domain: "spxsite.localhost", FilesDir: tmpDir, PublicDir: "/",
		PHPVersion: "8.3", DBEngine: "mysql", DBVersion: "8.0",
		DBPassword: "pw",
	}
}

// newSPXSiteManager wires a SiteManager onto an in-memory Storage. Only
// the fields SetSPXEnabled / RotateSPXKey / List+Clear touch are
// populated; Docker / router / hooks stay nil.
func newSPXSiteManager(t *testing.T) *SiteManager {
	t.Helper()
	st := storage.NewTestStorage(t)
	return &SiteManager{st: st}
}

func TestSetSPXEnabled_OnGeneratesKey(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}

	var updated *types.Site
	sm.OnSiteUpdated = func(s *types.Site) { updated = s }

	if err := sm.SetSPXEnabled(site.ID, true); err != nil {
		t.Fatalf("SetSPXEnabled(true): %v", err)
	}
	got, err := sm.st.GetSite(site.ID)
	if err != nil || got == nil {
		t.Fatalf("GetSite: %v / nil=%v", err, got == nil)
	}
	if !got.SPXEnabled {
		t.Error("SPXEnabled not persisted")
	}
	if got.SPXKey == "" {
		t.Error("SPXKey not generated on first enable")
	}
	if updated == nil || !updated.SPXEnabled || updated.SPXKey == "" {
		t.Error("OnSiteUpdated callback not fired with the new state")
	}
}

func TestSetSPXEnabled_OffPreservesKey(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	site.SPXEnabled = true
	site.SPXKey = "preexisting-key"
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}

	if err := sm.SetSPXEnabled(site.ID, false); err != nil {
		t.Fatalf("SetSPXEnabled(false): %v", err)
	}
	got, _ := sm.st.GetSite(site.ID)
	if got.SPXEnabled {
		t.Error("SPXEnabled still true after disable")
	}
	if got.SPXKey != "preexisting-key" {
		t.Errorf("SPXKey lost on disable: %q", got.SPXKey)
	}
}

func TestSetSPXEnabled_RejectsRunningSite(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	site.Started = true
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	if err := sm.SetSPXEnabled(site.ID, true); err == nil {
		t.Error("expected error toggling SPX on a running site, got nil")
	}
	got, _ := sm.st.GetSite(site.ID)
	if got.SPXEnabled {
		t.Error("SPXEnabled flipped on a running site despite the rejection")
	}
}

func TestSetSPXEnabled_NoOpWhenAlreadyInState(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	site.SPXEnabled = true
	site.SPXKey = "k"
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	called := false
	sm.OnSiteUpdated = func(*types.Site) { called = true }
	if err := sm.SetSPXEnabled(site.ID, true); err != nil {
		t.Fatalf("SetSPXEnabled: %v", err)
	}
	if called {
		t.Error("OnSiteUpdated fired despite no state change")
	}
}

func TestRotateSPXKey_ChangesKey(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	site.SPXEnabled = true
	site.SPXKey = "before"
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	if err := sm.RotateSPXKey(site.ID); err != nil {
		t.Fatalf("RotateSPXKey: %v", err)
	}
	got, _ := sm.st.GetSite(site.ID)
	if got.SPXKey == "" || got.SPXKey == "before" {
		t.Errorf("RotateSPXKey did not change the key (got %q)", got.SPXKey)
	}
}

func TestListSPXReports_NewestFirst(t *testing.T) {
	sm := newSPXSiteManager(t)
	dir := t.TempDir()
	site := spxTestSite(dir)
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}

	spxDir := filepath.Join(dir, ".locorum", "spx")
	if err := os.MkdirAll(spxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(spxDir, "old.txt")
	newer := filepath.Join(spxDir, "new.txt")
	if err := os.WriteFile(older, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}

	reports, err := sm.ListSPXReports(site.ID)
	if err != nil {
		t.Fatalf("ListSPXReports: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("len(reports) = %d, want 2", len(reports))
	}
	if reports[0].Name != "new.txt" {
		t.Errorf("reports[0].Name = %q, want new.txt", reports[0].Name)
	}
}

func TestListSPXReports_MissingDirIsEmpty(t *testing.T) {
	sm := newSPXSiteManager(t)
	site := spxTestSite(t.TempDir())
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	reports, err := sm.ListSPXReports(site.ID)
	if err != nil {
		t.Errorf("missing dir returned error: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("len(reports) = %d, want 0", len(reports))
	}
}

func TestClearSPXReports_RemovesAllFiles(t *testing.T) {
	sm := newSPXSiteManager(t)
	dir := t.TempDir()
	site := spxTestSite(dir)
	if err := sm.st.AddSite(&site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	spxDir := filepath.Join(dir, ".locorum", "spx")
	if err := os.MkdirAll(spxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(spxDir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := sm.ClearSPXReports(site.ID); err != nil {
		t.Fatalf("ClearSPXReports: %v", err)
	}
	entries, _ := os.ReadDir(spxDir)
	if len(entries) != 0 {
		t.Errorf("entries left after clear: %d", len(entries))
	}
}
