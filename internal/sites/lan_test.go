package sites

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/hooks"
	hooksfake "github.com/PeterBooker/locorum/internal/hooks/fake"
	routerfake "github.com/PeterBooker/locorum/internal/router/fake"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// memStore is a tiny in-memory Store used for typed Config wiring.
type memStore struct{ values map[string]string }

func (m *memStore) GetSetting(k string) (string, error) { return m.values[k], nil }
func (m *memStore) SetSetting(k, v string) error        { m.values[k] = v; return nil }

func newLanSiteManager(t *testing.T) (*SiteManager, *routerfake.Router, *hooksfake.Runner) {
	t.Helper()
	st := storage.NewTestStorage(t)
	cfg, err := config.New(&memStore{values: map[string]string{}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	rtr := routerfake.New()
	runner := hooksfake.New()
	sm := &SiteManager{
		st:    st,
		rtr:   rtr,
		hooks: runner,
		cfg:   cfg,
	}
	// Wire the real wp-config templates from disk. EnableLAN now
	// regenerates wp-config-locorum.php as part of its plan; without
	// a reachable template the apply-lan-state step would fail.
	wd, _ := os.Getwd()
	sm.SetTemplateReader(fileFS{root: filepath.Clean(filepath.Join(wd, "..", ".."))})
	// Force a deterministic LAN IP for routeFor + plan.
	sm.SetLANDetector(func() (net.IP, error) {
		return net.ParseIP("192.168.1.42").To4(), nil
	})
	return sm, rtr, runner
}

func newLanSite(t *testing.T, sm *SiteManager) *types.Site {
	t.Helper()
	site := &types.Site{
		ID: "lan-1", Name: "LAN", Slug: "lansite",
		Domain: "lansite.localhost", FilesDir: t.TempDir(), PublicDir: "/",
		PHPVersion: "8.3", DBEngine: "mysql", DBVersion: "8.0",
		DBPassword: "pw",
	}
	if err := sm.st.AddSite(site); err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	return site
}

func TestEnableLAN_FlipsRowAndIssuesRoute(t *testing.T) {
	sm, rtr, runner := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("EnableLAN: %v", err)
	}

	// Row updated.
	got, err := sm.st.GetSite(site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LanEnabled {
		t.Error("LanEnabled not persisted")
	}

	// Route upserted with LAN extras.
	routes := rtr.Sites()
	r, ok := routes["lansite"]
	if !ok {
		t.Fatalf("UpsertSite was not called; calls=%v", rtr.Calls())
	}
	if len(r.ExtraHosts) != 1 || r.ExtraHosts[0] != "lansite.192-168-1-42.sslip.io" {
		t.Errorf("ExtraHosts = %v, want [lansite.192-168-1-42.sslip.io]", r.ExtraHosts)
	}
	if len(r.ExtraWildcardHosts) != 0 {
		t.Errorf("ExtraWildcardHosts on non-multisite should be empty, got %v", r.ExtraWildcardHosts)
	}

	// Hook events fired pre/post.
	calls := runner.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 hook events, got %d (%v)", len(calls), calls)
	}
	if calls[0].Event != hooks.PreLanEnable || calls[1].Event != hooks.PostLanEnable {
		t.Errorf("unexpected hook events: %+v", calls)
	}
}

func TestEnableLAN_MultisiteEmitsWildcard(t *testing.T) {
	sm, rtr, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)
	site.Multisite = "subdomain"
	if _, err := sm.st.UpdateSite(site); err != nil {
		t.Fatal(err)
	}

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("EnableLAN: %v", err)
	}
	r := rtr.Sites()["lansite"]
	wantWild := "*.lansite.192-168-1-42.sslip.io"
	if len(r.ExtraWildcardHosts) != 1 || r.ExtraWildcardHosts[0] != wantWild {
		t.Errorf("ExtraWildcardHosts = %v, want [%s]", r.ExtraWildcardHosts, wantWild)
	}
	if r.WildcardHost != "*.lansite.localhost" {
		t.Errorf("primary WildcardHost lost: got %q", r.WildcardHost)
	}
}

func TestDisableLAN_RemovesExtras(t *testing.T) {
	sm, rtr, runner := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	runner.Reset()

	if err := sm.DisableLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("DisableLAN: %v", err)
	}

	got, _ := sm.st.GetSite(site.ID)
	if got.LanEnabled {
		t.Error("LanEnabled should be false after disable")
	}

	r := rtr.Sites()["lansite"]
	if len(r.ExtraHosts) != 0 {
		t.Errorf("ExtraHosts not cleared: %v", r.ExtraHosts)
	}
	if len(r.ExtraWildcardHosts) != 0 {
		t.Errorf("ExtraWildcardHosts not cleared: %v", r.ExtraWildcardHosts)
	}

	calls := runner.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 hook events, got %d", len(calls))
	}
	if calls[0].Event != hooks.PreLanDisable || calls[1].Event != hooks.PostLanDisable {
		t.Errorf("unexpected hook events: %+v", calls)
	}
}

func TestEnableLAN_NoUsableIPReturnsError(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	sm.SetLANDetector(func() (net.IP, error) {
		return nil, errors.New("no usable LAN IPv4 address found")
	})

	err := sm.EnableLAN(context.Background(), site.ID)
	if err == nil {
		t.Fatal("expected error when no LAN IP can be detected")
	}

	got, _ := sm.st.GetSite(site.ID)
	if got.LanEnabled {
		t.Error("LanEnabled must remain false on detection failure")
	}
}

func TestEnableLAN_RouteFailureRollsBack(t *testing.T) {
	sm, rtr, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	rtr.UpsertSiteErr = errors.New("traefik unhappy")
	defer func() { rtr.UpsertSiteErr = nil }()

	if err := sm.EnableLAN(context.Background(), site.ID); err == nil {
		t.Fatal("expected EnableLAN to fail when router upsert fails")
	}

	got, _ := sm.st.GetSite(site.ID)
	if got.LanEnabled {
		t.Error("LanEnabled should be reverted by rollback")
	}
}

func TestLanIPCached(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	calls := 0
	sm.SetLANDetector(func() (net.IP, error) {
		calls++
		return net.ParseIP("10.0.0.5").To4(), nil
	})
	for i := 0; i < 5; i++ {
		ip := sm.lanIP()
		if ip == nil || ip.String() != "10.0.0.5" {
			t.Fatalf("call %d got %v", i, ip)
		}
	}
	if calls != 1 {
		t.Errorf("detector hit %d times, want 1 (cache should absorb the rest)", calls)
	}
	sm.InvalidateLanIP()
	_ = sm.lanIP()
	if calls != 2 {
		t.Errorf("detector hit %d times after invalidate, want 2", calls)
	}
}

func TestLanIPRespectsConfigOverride(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	if err := sm.cfg.SetLanIPOverride("172.16.5.5"); err != nil {
		t.Fatal(err)
	}
	// Force the underlying detector to fail loudly so we can prove the
	// override path bypasses it.
	sm.SetLANDetector(func() (net.IP, error) {
		t.Fatal("detector should not be called when override is set")
		return nil, nil
	})
	ip := sm.lanIP()
	if ip == nil || ip.String() != "172.16.5.5" {
		t.Errorf("expected override 172.16.5.5, got %v", ip)
	}
}

func TestEnableLAN_RegeneratesWPConfigWithWhitelist(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("EnableLAN: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(site.FilesDir, "wp-config-locorum.php"))
	if err != nil {
		t.Fatalf("read wp-config-locorum.php: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "$locorum_primary_host = 'lansite.localhost'") {
		t.Errorf("primary host not baked into wp-config\n%s", s)
	}
	// phpEscape doubles backslashes so the rendered file has \\. where
	// the Go side computes \. — PHP parses that back to \. for PCRE.
	wantRe := `$locorum_lan_regex    = '/^lansite\\.\\d{1,3}-\\d{1,3}-\\d{1,3}-\\d{1,3}\\.sslip\\.io$/'`
	if !strings.Contains(s, wantRe) {
		t.Errorf("LAN regex not baked into wp-config\nwant: %s\n--- got ---\n%s", wantRe, s)
	}
}

func TestDisableLAN_RegeneratesWPConfigWithoutLAN(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	if err := sm.DisableLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("DisableLAN: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(site.FilesDir, "wp-config-locorum.php"))
	if !strings.Contains(string(body), "$locorum_lan_regex    = ''") {
		t.Errorf("LAN regex should be empty after disable\n%s", body)
	}
}

func TestRefreshLAN_RegensWPConfigAndReupsertsRoute(t *testing.T) {
	sm, rtr, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if err := sm.EnableLAN(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}

	// Swap the detected IP to a different one to prove the refresh
	// path picks it up.
	sm.SetLANDetector(func() (net.IP, error) {
		return net.ParseIP("10.5.5.5").To4(), nil
	})

	if err := sm.RefreshLAN(context.Background(), site.ID); err != nil {
		t.Fatalf("RefreshLAN: %v", err)
	}

	r := rtr.Sites()["lansite"]
	if len(r.ExtraHosts) != 1 || r.ExtraHosts[0] != "lansite.10-5-5-5.sslip.io" {
		t.Errorf("ExtraHosts after refresh = %v", r.ExtraHosts)
	}

	body, _ := os.ReadFile(filepath.Join(site.FilesDir, "wp-config-locorum.php"))
	if !strings.Contains(string(body), `'/^lansite\\.\\d{1,3}-\\d{1,3}-\\d{1,3}-\\d{1,3}\\.sslip\\.io$/'`) {
		t.Errorf("LAN regex missing after refresh\n%s", body)
	}
}

func TestRefreshLAN_RejectsWhenDisabled(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	err := sm.RefreshLAN(context.Background(), site.ID)
	if err == nil {
		t.Fatal("expected error for disabled site")
	}
}

func TestEnableLAN_RollbackRevertsWPConfig(t *testing.T) {
	sm, rtr, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	rtr.UpsertSiteErr = errors.New("traefik unhappy")
	defer func() { rtr.UpsertSiteErr = nil }()

	if err := sm.EnableLAN(context.Background(), site.ID); err == nil {
		t.Fatal("expected EnableLAN to fail")
	}

	// On rollback, wp-config should NOT contain the LAN regex — it
	// was reverted to the pre-toggle (disabled) state.
	body, _ := os.ReadFile(filepath.Join(site.FilesDir, "wp-config-locorum.php"))
	if !strings.Contains(string(body), "$locorum_lan_regex    = ''") {
		t.Errorf("wp-config not rolled back\n%s", body)
	}
}

func TestLanHostFor(t *testing.T) {
	sm, _, _ := newLanSiteManager(t)
	site := newLanSite(t, sm)

	if got := sm.LanHostFor(site); got != "" {
		t.Errorf("disabled site should give empty host, got %q", got)
	}
	site.LanEnabled = true
	want := "lansite.192-168-1-42.sslip.io"
	if got := sm.LanHostFor(site); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if got := sm.LanHostFor(nil); got != "" {
		t.Errorf("nil site should give empty host, got %q", got)
	}
}
