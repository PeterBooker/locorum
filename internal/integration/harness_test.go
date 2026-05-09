//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	dockercli "github.com/docker/docker/client"

	application "github.com/PeterBooker/locorum/internal/app"
	"github.com/PeterBooker/locorum/internal/config"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/hooks/fake"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/router/traefik"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/testutil"
	tlsfake "github.com/PeterBooker/locorum/internal/tls/fake"
)

// slugCounter ensures sibling tests producing slugs in the same second
// don't collide on the random suffix alone.
var slugCounter atomic.Int64

type harness struct {
	t         *testing.T
	ctx       context.Context
	homeDir   string
	cli       *dockercli.Client
	docker    *docker.Docker
	router    router.Router
	app       *application.App
	storage   *storage.Storage
	sites     *sites.SiteManager
	httpPort  int
	httpsPort int
}

// newHarness wires Locorum against a real Docker daemon in t.TempDir().
// Skipped when -short is set or the daemon is unreachable.
//
// Heavyweight: image pulls + container start can take 30s+ on a cold
// runner. Tests should call t.Parallel() to amortise.
func newHarness(t *testing.T) *harness {
	t.Helper()

	if testing.Short() {
		t.Skip("integration tests skipped in -short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	home := testutil.TempLocorumHome(t)

	// App.Initialize would normally extract embedded config; the
	// integration package has no embed.FS of its own, so seed the dir
	// from the repo tree directly.
	repoRoot := repoRootDir(t)
	if err := copyDir(filepath.Join(repoRoot, "config"), filepath.Join(home, ".locorum", "config")); err != nil {
		t.Fatalf("seed ~/.locorum/config: %v", err)
	}

	d := docker.New()
	if err := d.CheckDockerAvailable(ctx); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	cli, err := dockercli.NewClientWithOpts(dockercli.FromEnv, dockercli.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}

	httpPort, httpsPort := pickEphemeralPortPair(t)

	tlsProv := tlsfake.New()
	t.Cleanup(tlsProv.Cleanup)

	rtr, err := traefik.New(traefik.Config{
		HomeDir:    home,
		AppVersion: "integration-test",
		LogLevel:   "ERROR",
		HTTPPort:   httpPort,
		HTTPSPort:  httpsPort,
	}, d, tlsProv, os.DirFS(repoRoot))
	if err != nil {
		t.Fatalf("router init: %v", err)
	}

	// Empty embed.FS — assets/Reconcile walks an empty tree and the
	// pre-seeded config above is what survives.
	var emptyFS embed.FS
	app := application.New(emptyFS, d, home, rtr)
	if err := app.Initialize(ctx); err != nil {
		t.Fatalf("app initialize: %v", err)
	}
	t.Cleanup(func() {
		shCtx, shCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer shCancel()
		if err := app.Shutdown(shCtx); err != nil {
			t.Logf("app shutdown: %v", err)
		}
	})

	st := storage.NewTestStorage(t)
	cfg, err := config.New(st)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	sm := sites.NewSiteManager(st, cli, d, rtr, fake.New(), emptyFS, home, cfg)

	h := &harness{
		t:         t,
		ctx:       ctx,
		homeDir:   home,
		cli:       cli,
		docker:    d,
		router:    rtr,
		app:       app,
		storage:   st,
		sites:     sm,
		httpPort:  httpPort,
		httpsPort: httpsPort,
	}

	// Runs after App.Shutdown via t.Cleanup LIFO, so assertions reflect
	// the post-shutdown state of the daemon.
	t.Cleanup(func() {
		testutil.RequireNoDockerLeaks(h.t, dockerInspectorAdapter{d}, nil, "")
	})

	return h
}

// dockerInspectorAdapter wraps *docker.Docker as testutil.DockerInspector.
// Lives here rather than in testutil to keep that package free of
// docker-SDK imports.
type dockerInspectorAdapter struct{ d *docker.Docker }

func (a dockerInspectorAdapter) ContainersByLabel(ctx context.Context, match map[string]string) ([]docker.ContainerInfo, error) {
	return a.d.ContainersByLabel(ctx, match)
}
func (a dockerInspectorAdapter) NetworksByLabel(ctx context.Context, match map[string]string) ([]docker.NetworkInfo, error) {
	return a.d.NetworksByLabel(ctx, match)
}

func uniqueSlug(base string) string {
	id := slugCounter.Add(1)
	var buf [3]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("t-%s-%d-%s", base, id, hex.EncodeToString(buf[:]))
}

// pickEphemeralPortPair listens on :0 to ask the OS for a free port,
// then closes. Tiny TOCTOU race against another binder is preferable
// to hand-rolled allocation tracking.
func pickEphemeralPortPair(t *testing.T) (int, int) {
	t.Helper()
	pick := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen ephemeral: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		return port
	}
	return pick(), pick()
}

// repoRootDir uses runtime.Caller so the path is correct regardless of
// `go test` cwd.
func repoRootDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
