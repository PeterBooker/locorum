package health

import (
	"context"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/platform"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
)

// BundledOpts is the constructor input for [Bundled]. Each field maps to one
// or more bundled checks; passing nil for a dependency disables the checks
// that need it (so the runner can be partly wired up early in startup
// before all subsystems are ready).
type BundledOpts struct {
	// Platform is the *Info from internal/platform. Required for the
	// rosetta / WSL / Windows path checks.
	Platform *platform.Info

	// Engine is the docker engine. Required for the docker /
	// disk / port / virtiofs checks.
	Engine docker.Engine

	// Mkcert is the TLS provider. Optional — when nil the mkcert check
	// is omitted (the notice banner handles it instead).
	Mkcert tlspkg.Provider

	// MkcertInstaller is the one-click action. Pass nil to render the
	// finding without an action button.
	MkcertInstaller func(context.Context) error

	// Sites is the registered-sites lister. Optional — when nil the
	// path-shape checks (mnt-c, longpath) are omitted.
	Sites SiteRootLister

	// HostStatfsPath is the directory passed to platform.HostFreeBytes
	// for the disk-low check. Typically platform.Get().HomeDir; on
	// Windows native callers may want to pass the drive root.
	HostStatfsPath string

	// RouterContainerName is the docker container name the port-conflict
	// check inspects to identify "this is our router". Pass
	// traefik.ContainerName.
	RouterContainerName string

	// DiskWarnBytes / DiskBlockerBytes override the disk thresholds.
	// Zero values fall back to DefaultDiskWarnBytes /
	// DefaultDiskBlockerBytes.
	DiskWarnBytes    int64
	DiskBlockerBytes int64
}

// Bundled returns the production set of system-health checks. Wire-up:
//
//	runner := health.NewRunner(opts, health.Bundled(health.BundledOpts{
//	    Platform: plat,
//	    Engine:   d,
//	    Mkcert:   mkcert,
//	    MkcertInstaller: mkcertInstall,
//	    Sites:    sm,
//	    HostStatfsPath:      plat.HomeDir,
//	    RouterContainerName: traefik.ContainerName,
//	})...)
//
// Tests typically construct individual checks directly rather than going
// through Bundled.
func Bundled(opts BundledOpts) []Check {
	var out []Check

	if opts.Platform != nil {
		out = append(out, NewRosettaCheck(opts.Platform))
		if opts.Sites != nil {
			out = append(out,
				NewWSLMntCCheck(opts.Platform, opts.Sites),
				NewWindowsLongPathCheck(opts.Platform, opts.Sites),
			)
		}
	}

	if opts.Engine != nil {
		out = append(out,
			NewDockerDownCheck(opts.Engine),
			NewDockerOldCheck(opts.Engine),
		)
		if opts.Platform != nil {
			out = append(out, NewProviderVirtioFSCheck(opts.Engine, opts.Platform.OS))
		}
		out = append(out, NewDiskLowCheck(opts.Engine, DiskLowConfig{
			WarnBytes:    opts.DiskWarnBytes,
			BlockerBytes: opts.DiskBlockerBytes,
			Path:         opts.HostStatfsPath,
		}))
		if opts.RouterContainerName != "" {
			out = append(out,
				NewPortConflictCheck(opts.Engine, 80, opts.RouterContainerName),
				NewPortConflictCheck(opts.Engine, 443, opts.RouterContainerName),
			)
		}
	}

	if opts.Mkcert != nil {
		out = append(out, NewMkcertCheck(opts.Mkcert, opts.MkcertInstaller))
	}

	return out
}
