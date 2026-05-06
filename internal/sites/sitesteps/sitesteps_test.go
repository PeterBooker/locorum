package sitesteps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/docker/fake"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/types"
)

func testSite() *types.Site {
	return &types.Site{
		ID:           "id1",
		Name:         "demo",
		Slug:         "demo",
		Domain:       "demo.localhost",
		FilesDir:     "/tmp/demo",
		PHPVersion:   "8.2",
		DBEngine:     "mysql",
		DBVersion:    "8.4",
		MySQLVersion: "8.4",
		RedisVersion: "7",
		WebServer:    "nginx",
		DBPassword:   "p",
	}
}

func TestEnsureNetworkStep_Apply(t *testing.T) {
	eng := fake.New()
	site := testSite()

	step := &EnsureNetworkStep{Engine: eng, Site: site}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, ok := eng.Networks["locorum-demo"]; !ok {
		t.Errorf("network not created")
	}
}

func TestPullImagesStep_PullsAllSpecsOnce(t *testing.T) {
	eng := fake.New()
	site := testSite()
	specs := []docker.ContainerSpec{
		{Name: "a", Image: "nginx:1"},
		{Name: "b", Image: "nginx:1"}, // duplicate; should pull only once
		{Name: "c", Image: "mysql:8"},
	}

	step := &PullImagesStep{Engine: eng, Site: site, Specs: specs}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Pull dedupe — 2 unique images.
	if len(eng.Pulls) != 2 {
		t.Errorf("Pulls = %d, want 2 (deduped); got %v", len(eng.Pulls), eng.Pulls)
	}
}

func TestCreateContainersStep_CreatesAndStarts(t *testing.T) {
	eng := fake.New()
	specs := []docker.ContainerSpec{
		{Name: "x", Image: "nginx:1"},
		{Name: "y", Image: "mysql:8"},
	}
	step := &CreateContainersStep{Engine: eng, Specs: specs}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, name := range []string{"x", "y"} {
		c, ok := eng.Containers[name]
		if !ok {
			t.Errorf("container %q not created", name)
			continue
		}
		if !c.Running {
			t.Errorf("container %q not started", name)
		}
	}
}

func TestCreateContainersStep_RollbackRemovesAll(t *testing.T) {
	eng := fake.New()
	specs := []docker.ContainerSpec{{Name: "x", Image: "nginx:1"}}
	step := &CreateContainersStep{Engine: eng, Specs: specs}
	_ = step.Apply(context.Background())
	if err := step.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, ok := eng.Containers["x"]; ok {
		t.Errorf("rollback did not remove container")
	}
}

func TestPlanRollbackOnFailure(t *testing.T) {
	eng := fake.New()
	site := testSite()
	specs := []docker.ContainerSpec{{Name: "demo-web", Image: "nginx:1"}}

	plan := orch.Plan{
		Name: "test",
		Steps: []orch.Step{
			&EnsureNetworkStep{Engine: eng, Site: site},
			&CreateContainersStep{Engine: eng, Specs: specs},
			// Force failure to trigger rollback.
			&FuncStep{
				Label: "fail-here",
				Do:    func(_ context.Context) error { return errors.New("boom") },
			},
		},
	}
	res := orch.Run(context.Background(), plan, orch.Callbacks{})
	if !res.RolledBack {
		t.Errorf("RolledBack = false, want true")
	}
	if res.FinalError == nil {
		t.Errorf("expected FinalError")
	}
	// Network was rolled back.
	if _, ok := eng.Networks["locorum-demo"]; ok {
		t.Errorf("network still present after rollback")
	}
	// Containers rolled back.
	if _, ok := eng.Containers["demo-web"]; ok {
		t.Errorf("container still present after rollback")
	}
}

func TestWaitReadyStep_PerContainerTimeouts(t *testing.T) {
	eng := fake.New()
	step := &WaitReadyStep{
		Engine:     eng,
		Containers: []string{"a", "b"},
	}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(eng.WaitedFor) != 2 {
		t.Errorf("WaitedFor = %v, want 2 entries", eng.WaitedFor)
	}
}

func TestEngineFakeImplementsInterface(t *testing.T) {
	var _ docker.Engine = fake.New()
}

func TestEnsureSPXStep_Disabled_NoOp(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	site := &types.Site{Slug: "demo", FilesDir: dir, SPXEnabled: false}
	step := &EnsureSPXStep{Site: site, HomeDir: home}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".locorum", "spx")); !os.IsNotExist(err) {
		t.Errorf("data dir created when SPX disabled (err=%v)", err)
	}
	if _, err := os.Stat(docker.SPXKeyINIPath(home, site.Slug)); !os.IsNotExist(err) {
		t.Errorf("key INI created when SPX disabled (err=%v)", err)
	}
}

func TestEnsureSPXStep_Disabled_RemovesStaleKey(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	site := &types.Site{Slug: "demo", FilesDir: dir, SPXEnabled: false}
	keyPath := docker.SPXKeyINIPath(home, site.Slug)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("spx.http_key = stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	step := &EnsureSPXStep{Site: site, HomeDir: home}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("stale key INI not removed on disabled toggle (err=%v)", err)
	}
}

func TestEnsureSPXStep_Enabled_WritesKeyAndDataDir(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	site := &types.Site{Slug: "demo", FilesDir: dir, SPXEnabled: true, SPXKey: "abcDEF-_123"}
	step := &EnsureSPXStep{Site: site, HomeDir: home}

	for i := 0; i < 2; i++ {
		if err := step.Apply(context.Background()); err != nil {
			t.Fatalf("Apply iter %d: %v", i, err)
		}
	}

	dataDir := filepath.Join(dir, ".locorum", "spx")
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		t.Errorf("data dir not created: err=%v info=%v", err, info)
	}

	keyPath := docker.SPXKeyINIPath(home, site.Slug)
	body, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key INI: %v", err)
	}
	if !contains(string(body), "spx.http_key = abcDEF-_123") {
		t.Errorf("key INI missing spx.http_key line: %q", body)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key INI: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key INI mode = %o, want 0600", mode)
	}

	// Rollback must NOT delete: data dir might hold reports.
	if err := step.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("rollback removed the SPX data dir: %v", err)
	}
}

func TestEnsureSPXStep_KeyChangeIsPickedUp(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	site := &types.Site{Slug: "demo", FilesDir: dir, SPXEnabled: true, SPXKey: "first"}
	step := &EnsureSPXStep{Site: site, HomeDir: home}
	if err := step.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	site.SPXKey = "second"
	if err := step.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(docker.SPXKeyINIPath(home, site.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(body), "spx.http_key = second") {
		t.Errorf("rotated key not written: %q", body)
	}
	if contains(string(body), "spx.http_key = first") {
		t.Errorf("old key still present: %q", body)
	}
}

// contains is a tiny string-contains helper so tests stay free of
// strings.Contains imports per the existing pattern in this file.
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
