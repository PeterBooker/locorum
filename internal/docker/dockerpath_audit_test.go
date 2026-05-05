package docker

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/platform"
	"github.com/PeterBooker/locorum/internal/types"
)

// TestSpecBuildersBindMountsAreSlashSafe runs every spec-builder under
// realistic input and confirms each BindMount.Source survives a round-trip
// through platform.DockerPath unchanged. If a future spec edit introduces
// an unsanitised Windows-shaped path or a `filepath.Join` that surfaces
// backslashes on the build host, this test fails — replacing the manual
// audit pass that decays over time.
//
// We deliberately don't compare against the *exact* output of
// platform.DockerPath in production-realistic Linux fixtures because all
// the existing builders already pass paths through filepath.Join (which
// renders forward slashes on Linux). The test is the regression bar:
// builders must produce strings that DockerPath leaves alone, which is
// the contract every "production" path satisfies.
func TestSpecBuildersBindMountsAreSlashSafe(t *testing.T) {
	site := &types.Site{
		ID:       "1",
		Slug:     "demo",
		Name:     "Demo",
		FilesDir: filepath.Join("/home/u", "locorum", "sites", "demo"),
	}
	homeDir := "/home/u"

	specs := []ContainerSpec{
		NginxWebSpec(site, homeDir),
		ApacheWebSpec(site, homeDir),
		WebSpec(site, homeDir),
		PHPSpec(site, homeDir),
		RedisSpec(site),
		MailSpec(),
		AdminerSpec(),
	}
	for _, s := range specs {
		for i, m := range s.Mounts {
			if m.Bind == nil {
				continue
			}
			src := m.Bind.Source
			if strings.ContainsRune(src, '\\') {
				t.Errorf("%s mount[%d]: contains backslash %q — wrap in platform.DockerPath", s.Name, i, src)
			}
			if got := platform.DockerPath(src); got != src {
				t.Errorf("%s mount[%d]: not slash-canonical: src=%q DockerPath=%q", s.Name, i, src, got)
			}
		}
	}
}

// TestBuildMountsCallsDockerPath is the canary for the central
// translation point. We hand a Windows-shaped path through buildMounts and
// confirm the result has no backslashes — even on a Linux test runner
// where filepath.ToSlash is otherwise a no-op.
func TestBuildMountsCallsDockerPath(t *testing.T) {
	in := []Mount{
		{Bind: &BindMount{Source: `C:\Users\Peter\sites\demo`, Target: "/var/www/html"}},
		{Bind: &BindMount{Source: `D:\config\nginx.conf`, Target: "/etc/nginx/nginx.conf", ReadOnly: true}},
	}
	binds, _ := buildMounts(in)
	if len(binds) != 2 {
		t.Fatalf("expected 2 binds, got %d", len(binds))
	}
	for _, b := range binds {
		if strings.ContainsRune(b, '\\') {
			t.Errorf("buildMounts left a backslash in %q", b)
		}
	}
	want := "C:/Users/Peter/sites/demo:/var/www/html"
	if binds[0] != want {
		t.Errorf("first bind = %q, want %q", binds[0], want)
	}
	wantRO := "D:/config/nginx.conf:/etc/nginx/nginx.conf:ro"
	if binds[1] != wantRO {
		t.Errorf("second bind = %q, want %q", binds[1], wantRO)
	}
}
