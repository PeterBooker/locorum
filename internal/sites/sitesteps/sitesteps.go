// Package sitesteps assembles the per-site lifecycle Plan from individual
// orch.Step implementations. Each step is small, named, and has a clearly
// defined Apply/Rollback. SiteManager builds Plans from these and runs them
// via orch.Run.
//
// Dependency direction:
//
//	internal/sites/sitesteps
//	    │
//	    ▼ uses
//	internal/orch  +  internal/docker  +  internal/router
//
// Steps are not aware of each other — communication happens through the
// site struct and the engine's idempotent state. A step that runs after
// another doesn't read its outputs; it inspects the world.
package sitesteps

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/types"
	"golang.org/x/sync/errgroup"
)

// readyTimeout per service. Conservative; LOCORUM_HEALTH_TIMEOUT_MULT
// scales these for slow CI.
const (
	readyTimeoutDB    = 120 * time.Second
	readyTimeoutOther = 60 * time.Second

	// pullParallelism caps concurrent image pulls so a fresh install on a
	// slow connection doesn't saturate the user's pipe. 4 is conservative
	// — site-start needs nginx + php + mysql + redis = 4 images.
	pullParallelism = 4

	// stopGrace is the grace period before SIGKILL when stopping a
	// service container.
	stopGrace = 10 * time.Second
)

// EnsureNetworkStep creates the per-site bridge network if absent.
type EnsureNetworkStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *EnsureNetworkStep) Name() string { return "ensure-network" }
func (s *EnsureNetworkStep) Apply(ctx context.Context) error {
	_, err := s.Engine.EnsureNetwork(ctx, docker.SiteNetworkSpec(s.Site))
	return err
}
func (s *EnsureNetworkStep) Rollback(ctx context.Context) error {
	// Only remove the network on rollback if no containers are still
	// attached to it; otherwise we'd orphan them. The label-based
	// RemoveNetworksByLabel call already filters to networks owned by this
	// site, so it's site-scoped.
	return s.Engine.RemoveNetworksByLabel(ctx, map[string]string{
		docker.LabelPlatform: docker.PlatformValue,
		docker.LabelSite:     s.Site.Slug,
		docker.LabelRole:     docker.RoleSiteNetwork,
	})
}

// EnsureVolumeStep creates the database data volume if absent.
type EnsureVolumeStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *EnsureVolumeStep) Name() string { return "ensure-volume" }
func (s *EnsureVolumeStep) Apply(ctx context.Context) error {
	_, err := s.Engine.EnsureVolume(ctx, docker.SiteVolumeSpec(s.Site))
	return err
}
func (s *EnsureVolumeStep) Rollback(_ context.Context) error {
	// Volumes are preserved by design — losing user DB data on a transient
	// start failure would be a much worse outcome than leaving an unused
	// volume around. Cleanup is the user's explicit "purge" action only.
	return nil
}

// PullImagesStep pulls every image required by the site, in parallel up to
// the configured limit. onProgress is called per pull tick so the GUI can
// show "Pulling nginx — 12.4 MB / 18.7 MB".
type PullImagesStep struct {
	Engine     docker.Engine
	Site       *types.Site
	Specs      []docker.ContainerSpec
	OnProgress func(docker.PullProgress)
}

func (s *PullImagesStep) Name() string { return "pull-images" }
func (s *PullImagesStep) Apply(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(pullParallelism)
	seen := map[string]bool{}
	var mu sync.Mutex
	for _, spec := range s.Specs {
		ref := spec.Image
		mu.Lock()
		if seen[ref] {
			mu.Unlock()
			continue
		}
		seen[ref] = true
		mu.Unlock()

		g.Go(func() error {
			if err := s.Engine.PullImage(gctx, ref, s.OnProgress); err != nil {
				return fmt.Errorf("pull %s: %w", ref, err)
			}
			return nil
		})
	}
	return g.Wait()
}
func (s *PullImagesStep) Rollback(_ context.Context) error {
	// Pulled images stay cached; cleanup is `docker image prune` if the
	// user wants it. No-op here.
	return nil
}

// ChownStep recursively chowns the DB volume and the site files dir to the
// PHP UID/GID before service containers start. Without this, mysql can fail
// to write to its data dir on rootless Docker, and PHP-FPM can't write to
// wp-content/uploads.
type ChownStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *ChownStep) Name() string { return "chown" }
func (s *ChownStep) Apply(ctx context.Context) error {
	uid, gid := docker.PHPUserGroup()

	// Chown the DB volume contents to a uid:gid that mysql can write as.
	// MySQL's image initialises its data dir as uid 999 on first start, but
	// our chown needs to happen against an existing volume — we run the
	// chown as the PHP uid since that's the user that needs to read/write
	// shared data. mysql init handles its own perms once started.
	if err := s.Engine.ChownVolume(ctx, docker.SiteVolumeName(s.Site.Slug), uid, gid); err != nil {
		return fmt.Errorf("chown db volume: %w", err)
	}

	// Chown the site files directory.
	if err := s.Engine.ChownPath(ctx, s.Site.FilesDir, uid, gid); err != nil {
		return fmt.Errorf("chown files dir: %w", err)
	}
	return nil
}
func (s *ChownStep) Rollback(_ context.Context) error { return nil }

// CreateContainersStep creates and starts the four service containers in
// parallel within a single tier. Containers are application-dependent
// (PHP needs DB *connectivity*) but Docker-startup-order independent —
// each starts in seconds, then the WaitReadyStep tier handles readiness.
type CreateContainersStep struct {
	Engine docker.Engine
	Specs  []docker.ContainerSpec
}

func (s *CreateContainersStep) Name() string { return "create-containers" }
func (s *CreateContainersStep) Apply(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, spec := range s.Specs {
		g.Go(func() error {
			if _, err := s.Engine.EnsureContainer(gctx, spec); err != nil {
				return fmt.Errorf("ensure %s: %w", spec.Name, err)
			}
			if err := s.Engine.StartContainer(gctx, spec.Name); err != nil {
				return fmt.Errorf("start %s: %w", spec.Name, err)
			}
			return nil
		})
	}
	return g.Wait()
}
func (s *CreateContainersStep) Rollback(ctx context.Context) error {
	// Reverse order is irrelevant for containers — they all join the same
	// site network. Force-remove each in parallel.
	g, gctx := errgroup.WithContext(ctx)
	for _, spec := range s.Specs {
		g.Go(func() error {
			if err := s.Engine.RemoveContainer(gctx, spec.Name); err != nil {
				slog.Warn("rollback: remove container", "name", spec.Name, "err", err.Error())
			}
			return nil
		})
	}
	return g.Wait()
}

// WaitReadyStep waits for every named container to report healthy. Runs
// per-container waits in parallel so the slowest determines the deadline.
type WaitReadyStep struct {
	Engine     docker.Engine
	Containers []string
	// Per-container override for timeout. Defaults to readyTimeoutOther
	// for any name not in this map; the database name should map to
	// readyTimeoutDB.
	Timeouts map[string]time.Duration
}

func (s *WaitReadyStep) Name() string { return "wait-ready" }
func (s *WaitReadyStep) Apply(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range s.Containers {
		timeout := readyTimeoutOther
		if t, ok := s.Timeouts[name]; ok {
			timeout = t
		}
		g.Go(func() error {
			return s.Engine.WaitReady(gctx, name, timeout)
		})
	}
	return g.Wait()
}
func (s *WaitReadyStep) Rollback(_ context.Context) error { return nil }

// StopContainersStep stops every named container with a grace period.
type StopContainersStep struct {
	Engine     docker.Engine
	Containers []string
}

func (s *StopContainersStep) Name() string { return "stop-containers" }
func (s *StopContainersStep) Apply(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range s.Containers {
		g.Go(func() error {
			if err := s.Engine.StopContainer(gctx, name, stopGrace); err != nil {
				return fmt.Errorf("stop %s: %w", name, err)
			}
			return nil
		})
	}
	return g.Wait()
}
func (s *StopContainersStep) Rollback(_ context.Context) error { return nil }

// RemoveContainersStep force-removes every named container.
type RemoveContainersStep struct {
	Engine     docker.Engine
	Containers []string
}

func (s *RemoveContainersStep) Name() string { return "remove-containers" }
func (s *RemoveContainersStep) Apply(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range s.Containers {
		g.Go(func() error {
			if err := s.Engine.RemoveContainer(gctx, name); err != nil {
				return fmt.Errorf("remove %s: %w", name, err)
			}
			return nil
		})
	}
	return g.Wait()
}
func (s *RemoveContainersStep) Rollback(_ context.Context) error { return nil }

// RegisterRoutesStep upserts the site's routes into the global router.
type RegisterRoutesStep struct {
	Router router.Router
	Route  router.SiteRoute
}

func (s *RegisterRoutesStep) Name() string { return "register-routes" }
func (s *RegisterRoutesStep) Apply(ctx context.Context) error {
	return s.Router.UpsertSite(ctx, s.Route)
}
func (s *RegisterRoutesStep) Rollback(ctx context.Context) error {
	return s.Router.RemoveSite(ctx, s.Route.Slug)
}

// RemoveRoutesStep removes the site's routes from the global router.
type RemoveRoutesStep struct {
	Router router.Router
	Slug   string
}

func (s *RemoveRoutesStep) Name() string { return "remove-routes" }
func (s *RemoveRoutesStep) Apply(ctx context.Context) error {
	return s.Router.RemoveSite(ctx, s.Slug)
}
func (s *RemoveRoutesStep) Rollback(_ context.Context) error { return nil }

// RemoveNetworkStep removes the per-site network. Volumes are not removed.
type RemoveNetworkStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *RemoveNetworkStep) Name() string { return "remove-network" }
func (s *RemoveNetworkStep) Apply(ctx context.Context) error {
	return s.Engine.RemoveNetworksByLabel(ctx, map[string]string{
		docker.LabelPlatform: docker.PlatformValue,
		docker.LabelSite:     s.Site.Slug,
		docker.LabelRole:     docker.RoleSiteNetwork,
	})
}
func (s *RemoveNetworkStep) Rollback(_ context.Context) error { return nil }

// RemoveSiteConfigsStep removes the per-site nginx/apache config files.
type RemoveSiteConfigsStep struct {
	HomeDir string
	Site    *types.Site
}

func (s *RemoveSiteConfigsStep) Name() string { return "remove-site-configs" }
func (s *RemoveSiteConfigsStep) Apply(_ context.Context) error {
	return removeIgnoringMissing(
		filepath.Join(s.HomeDir, ".locorum", "config", "nginx", "sites", s.Site.Slug+".conf"),
		filepath.Join(s.HomeDir, ".locorum", "config", "apache", "sites", s.Site.Slug+".conf"),
	)
}
func (s *RemoveSiteConfigsStep) Rollback(_ context.Context) error { return nil }

// PurgeVolumeStep removes the site's database volume. Used only by the
// "purge" path in the three-way delete UI.
type PurgeVolumeStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *PurgeVolumeStep) Name() string { return "purge-volume" }
func (s *PurgeVolumeStep) Apply(ctx context.Context) error {
	// Volumes are removed by label, mirroring the rest of the engine's
	// label-based discovery. Any same-name leftover from a pre-label
	// install is also caught.
	return removeVolumeByLabel(ctx, s.Engine, s.Site.Slug)
}
func (s *PurgeVolumeStep) Rollback(_ context.Context) error { return nil }

// HookStep wraps a hooks-runner invocation as an orch.Step. The runner is
// already context-aware; we just defer Apply to it.
type HookStep struct {
	Label string
	Run   func(ctx context.Context) error
}

func (s *HookStep) Name() string                     { return s.Label }
func (s *HookStep) Apply(ctx context.Context) error  { return s.Run(ctx) }
func (s *HookStep) Rollback(_ context.Context) error { return nil }

// FuncStep is an inline adapter for one-off side effects (e.g. updating the
// SQL row, ensuring WordPress files are present). Prefer named struct steps
// for substantial logic; FuncStep is for short glue.
type FuncStep struct {
	Label string
	Do    func(ctx context.Context) error
	Undo  func(ctx context.Context) error
}

func (s *FuncStep) Name() string { return s.Label }
func (s *FuncStep) Apply(ctx context.Context) error {
	if s.Do == nil {
		return nil
	}
	return s.Do(ctx)
}
func (s *FuncStep) Rollback(ctx context.Context) error {
	if s.Undo == nil {
		return nil
	}
	return s.Undo(ctx)
}

// Compile-time guard: every Step type satisfies orch.Step.
var (
	_ orch.Step = (*EnsureNetworkStep)(nil)
	_ orch.Step = (*EnsureVolumeStep)(nil)
	_ orch.Step = (*PullImagesStep)(nil)
	_ orch.Step = (*ChownStep)(nil)
	_ orch.Step = (*CreateContainersStep)(nil)
	_ orch.Step = (*WaitReadyStep)(nil)
	_ orch.Step = (*StopContainersStep)(nil)
	_ orch.Step = (*RemoveContainersStep)(nil)
	_ orch.Step = (*RegisterRoutesStep)(nil)
	_ orch.Step = (*RemoveRoutesStep)(nil)
	_ orch.Step = (*RemoveNetworkStep)(nil)
	_ orch.Step = (*RemoveSiteConfigsStep)(nil)
	_ orch.Step = (*PurgeVolumeStep)(nil)
	_ orch.Step = (*HookStep)(nil)
	_ orch.Step = (*FuncStep)(nil)
)
