package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// tempSockPath returns a unix-socket path short enough to fit in the
// 104-byte sun_path limit on macOS, where t.TempDir() lives under
// /var/folders/... and routinely exceeds it.
func tempSockPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "darwin" {
		dir, err := os.MkdirTemp("/tmp", "lcr")
		if err != nil {
			t.Fatalf("MkdirTemp: %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return filepath.Join(dir, "s")
	}
	return filepath.Join(t.TempDir(), "ipc.sock")
}

// fakeService implements SiteService for round-trip tests. Just enough
// to exercise the dispatcher's parameter parsing, scope enforcement,
// and JSON-RPC framing.
type fakeService struct {
	sites    []types.Site
	startErr error

	startedID string
	stoppedID string
}

func (f *fakeService) DescribeAll(_ context.Context, _ sites.DescribeOptions) ([]sites.SiteDescription, error) {
	out := make([]sites.SiteDescription, len(f.sites))
	for i, s := range f.sites {
		out[i] = sites.SiteDescription{ID: s.ID, Slug: s.Slug, Name: s.Name, URL: "https://" + s.Domain}
	}
	return out, nil
}

func (f *fakeService) Describe(_ context.Context, id string, _ sites.DescribeOptions) (*sites.SiteDescription, error) {
	for _, s := range f.sites {
		if s.ID == id {
			return &sites.SiteDescription{ID: s.ID, Slug: s.Slug, Name: s.Name}, nil
		}
	}
	return nil, nil
}

func (f *fakeService) StartSite(_ context.Context, id string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.startedID = id
	return nil
}

func (f *fakeService) StopSite(_ context.Context, id string) error {
	f.stoppedID = id
	return nil
}

func (f *fakeService) RecentActivity(_ string) ([]storage.ActivityEvent, error) { return nil, nil }
func (f *fakeService) GetActivity(_ string, _ int) ([]storage.ActivityEvent, error) {
	return nil, nil
}
func (f *fakeService) GetContainerLogs(_ context.Context, _, _ string, _ int) (string, error) {
	return "", nil
}
func (f *fakeService) ExecWPCLI(_ context.Context, _ string, _ []string) (string, error) {
	return "", nil
}
func (f *fakeService) Snapshot(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}
func (f *fakeService) ListSnapshots(_ string) ([]sites.SnapshotInfo, error) { return nil, nil }
func (f *fakeService) RestoreSnapshot(_ context.Context, _, _ string, _ sites.RestoreSnapshotOptions) error {
	return nil
}
func (f *fakeService) CreateWorktreeSite(_ context.Context, _ sites.CreateWorktreeOptions) (*sites.CreateWorktreeResult, error) {
	return nil, nil
}
func (f *fakeService) DeleteSiteWithOptions(_ context.Context, _ string, _ sites.DeleteOptions) error {
	return nil
}
func (f *fakeService) RunHookNow(_ context.Context, _ hooks.Hook) (hooks.Result, error) {
	return hooks.Result{}, nil
}
func (f *fakeService) ListSiteHooks(_ string) ([]hooks.Hook, error) { return nil, nil }
func (f *fakeService) GetSites() ([]types.Site, error)              { return f.sites, nil }

// startTestServer wires a Server + Listener and returns a connected
// client. Both are torn down at t.Cleanup.
func startTestServer(t *testing.T, svc SiteService) *Client {
	t.Helper()

	sock := tempSockPath(t)

	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	srv := NewServer(ln, nil)
	RegisterMethods(srv, svc)

	srvCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(srvCtx) }()

	t.Cleanup(func() {
		cancel()
		srv.Shutdown(time.Second)
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	defer dialCancel()
	cli, err := DialClient(dialCtx, sock, HelloOptions{PeerKind: "test"})
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func TestServer_RoundTrip_SiteList(t *testing.T) {
	svc := &fakeService{
		sites: []types.Site{
			{ID: "id1", Slug: "first", Name: "First", Domain: "first.localhost"},
			{ID: "id2", Slug: "second", Name: "Second", Domain: "second.localhost"},
		},
	}
	cli := startTestServer(t, svc)

	var out []sites.SiteDescription
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cli.Call(ctx, "site.list", map[string]any{}, &out); err != nil {
		t.Fatalf("Call site.list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d sites, want 2", len(out))
	}
	if out[0].Slug != "first" {
		t.Fatalf("first site slug: %q", out[0].Slug)
	}
}

func TestServer_RoundTrip_SiteStart_BySlug(t *testing.T) {
	svc := &fakeService{
		sites: []types.Site{{ID: "id1", Slug: "shop", Name: "Shop"}},
	}
	cli := startTestServer(t, svc)

	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cli.Call(ctx, "site.start", map[string]any{"slug": "shop"}, &out); err != nil {
		t.Fatalf("Call site.start: %v", err)
	}
	if svc.startedID != "id1" {
		t.Fatalf("StartSite was not called with the right id: got %q", svc.startedID)
	}
}

func TestServer_NotFound_BySlug(t *testing.T) {
	svc := &fakeService{}
	cli := startTestServer(t, svc)

	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := cli.Call(ctx, "site.start", map[string]any{"slug": "missing"}, &out)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T", err)
	}
	if rpcErr.Code != CodeNotFound {
		t.Fatalf("expected CodeNotFound (%d), got %d", CodeNotFound, rpcErr.Code)
	}
}

func TestServer_ReadOnlyRejectsMutating(t *testing.T) {
	svc := &fakeService{
		sites: []types.Site{{ID: "id1", Slug: "shop"}},
	}

	sock := tempSockPath(t)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := NewServer(ln, nil)
	RegisterMethods(srv, svc)

	srvCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(srvCtx) }()
	t.Cleanup(func() {
		cancel()
		srv.Shutdown(time.Second)
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	defer dialCancel()
	cli, err := DialClient(dialCtx, sock, HelloOptions{PeerKind: "test", Profile: ProfileReadOnly})
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	defer cli.Close()

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()
	var out map[string]any
	err = cli.Call(ctx, "site.start", map[string]any{"slug": "shop"}, &out)
	if err == nil {
		t.Fatalf("expected forbidden error, got nil")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T", err)
	}
	if rpcErr.Code != CodeForbidden {
		t.Fatalf("expected CodeForbidden, got %d", rpcErr.Code)
	}

	// Read-only methods still work.
	var listOut []sites.SiteDescription
	if err := cli.Call(ctx, "site.list", map[string]any{}, &listOut); err != nil {
		t.Fatalf("readonly site.list: %v", err)
	}
}

func TestServer_MCPScope_RejectsWrongSite(t *testing.T) {
	svc := &fakeService{
		sites: []types.Site{
			{ID: "id1", Slug: "scoped"},
			{ID: "id2", Slug: "other"},
		},
	}

	sock := tempSockPath(t)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := NewServer(ln, nil)
	RegisterMethods(srv, svc)

	srvCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(srvCtx) }()
	t.Cleanup(func() {
		cancel()
		srv.Shutdown(time.Second)
	})

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	defer dialCancel()
	cli, err := DialClient(dialCtx, sock, HelloOptions{
		PeerKind: "mcp",
		MCPScope: "scoped",
	})
	if err != nil {
		t.Fatalf("DialClient: %v", err)
	}
	defer cli.Close()

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()
	var out map[string]any
	// Ask to start a different site than the scope. Should be refused.
	err = cli.Call(ctx, "site.start", map[string]any{"slug": "other"}, &out)
	if err == nil {
		t.Fatalf("expected scope rejection, got nil")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T", err)
	}
	if rpcErr.Code != CodeForbidden {
		t.Fatalf("expected CodeForbidden, got %d (msg=%s)", rpcErr.Code, rpcErr.Message)
	}
	if svc.startedID != "" {
		t.Fatalf("StartSite was reached despite scope rejection")
	}

	// Sanity: scoped slug is allowed.
	if err := cli.Call(ctx, "site.start", map[string]any{"slug": "scoped"}, &out); err != nil {
		t.Fatalf("scoped slug should succeed, got %v", err)
	}
	if svc.startedID != "id1" {
		t.Fatalf("expected startedID=id1, got %q", svc.startedID)
	}
}
