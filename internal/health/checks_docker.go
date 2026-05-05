package health

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/version"
)

// DockerDownCheck pings the Docker daemon. A single ping failure is not
// enough to declare Docker down — daemons restart, sockets glitch, transient
// network errors happen. We require two consecutive failures to surface a
// blocker. The state machine is internal to the check and reset on a
// successful ping.
type DockerDownCheck struct {
	engine docker.Engine

	// failures is the consecutive-fail counter. Atomic so the runner's
	// goroutine can race against an in-flight RunNow without locking.
	failures atomic.Int32
}

// NewDockerDownCheck builds the check.
func NewDockerDownCheck(engine docker.Engine) *DockerDownCheck {
	return &DockerDownCheck{engine: engine}
}

func (*DockerDownCheck) ID() string             { return "docker-down" }
func (*DockerDownCheck) Cadence() time.Duration { return 0 }
func (*DockerDownCheck) Budget() time.Duration  { return 3 * time.Second }

func (c *DockerDownCheck) Run(ctx context.Context) ([]Finding, error) {
	err := c.engine.Ping(ctx)
	if err == nil {
		c.failures.Store(0)
		return nil, nil
	}
	n := c.failures.Add(1)
	if n < 2 {
		// First failure — silent so a daemon restart blip doesn't
		// surface a transient blocker.
		return nil, nil
	}
	return []Finding{{
		ID:          c.ID(),
		Severity:    SeverityBlocker,
		Title:       "Docker is not running",
		Detail:      fmt.Sprintf("Locorum could not reach the Docker daemon: %s", err.Error()),
		Remediation: "Start Docker Desktop / OrbStack / your Docker engine and re-check.",
		HelpURL:     "https://docs.locorum.dev/install/docker",
	}}, nil
}

// DockerOldCheck warns when the daemon is older than [version.MinSupported*].
// Patch-level differences are ignored; we only care about Major.Minor.
type DockerOldCheck struct {
	engine docker.Engine
}

// NewDockerOldCheck builds the check.
func NewDockerOldCheck(engine docker.Engine) *DockerOldCheck {
	return &DockerOldCheck{engine: engine}
}

func (*DockerOldCheck) ID() string             { return "docker-old" }
func (*DockerOldCheck) Cadence() time.Duration { return 30 * time.Minute }
func (*DockerOldCheck) Budget() time.Duration  { return 2 * time.Second }

func (c *DockerOldCheck) Run(ctx context.Context) ([]Finding, error) {
	pi, err := c.engine.ProviderInfo(ctx)
	if err != nil {
		// Don't surface "old daemon" when we don't even know the
		// version — that's docker-down territory.
		return nil, nil
	}
	if pi.ServerVersionP.IsZero() {
		return nil, nil
	}
	if !pi.ServerVersionP.LessThan(version.MinSupportedDockerServerMajor, version.MinSupportedDockerServerMinor) {
		return nil, nil
	}
	return []Finding{{
		ID:       c.ID(),
		Severity: SeverityWarn,
		Title:    "Docker is older than recommended",
		Detail: fmt.Sprintf("Daemon reports version %s; Locorum is tested against %d.%d and newer.",
			pi.ServerVersion, version.MinSupportedDockerServerMajor, version.MinSupportedDockerServerMinor),
		Remediation: "Update Docker Desktop / OrbStack / your engine.",
		HelpURL:     "https://docs.docker.com/engine/release-notes/",
	}}, nil
}

// ProviderVirtioFSCheck warns macOS Docker Desktop users about VirtioFS's
// slow-bind-mount behaviour for the WordPress workload. Stage 1 of the
// LEARNINGS §6.3 mutagen roadmap — detection + advisory only.
type ProviderVirtioFSCheck struct {
	engine docker.Engine
	info   func() *struct{ OS string }
}

// NewProviderVirtioFSCheck builds the check using a docker.Engine.
func NewProviderVirtioFSCheck(engine docker.Engine, hostOS string) *ProviderVirtioFSCheck {
	c := &ProviderVirtioFSCheck{engine: engine}
	hostOSCopy := hostOS
	c.info = func() *struct{ OS string } { return &struct{ OS string }{OS: hostOSCopy} }
	return c
}

func (*ProviderVirtioFSCheck) ID() string             { return "provider-virtiofs" }
func (*ProviderVirtioFSCheck) Cadence() time.Duration { return 30 * time.Minute }
func (*ProviderVirtioFSCheck) Budget() time.Duration  { return 2 * time.Second }

func (c *ProviderVirtioFSCheck) Run(ctx context.Context) ([]Finding, error) {
	host := c.info()
	if host == nil || host.OS != "darwin" {
		return nil, nil
	}
	pi, err := c.engine.ProviderInfo(ctx)
	if err != nil {
		return nil, nil
	}
	if !pi.IsDockerDesktop {
		return nil, nil
	}
	return []Finding{{
		ID:       c.ID(),
		Severity: SeverityInfo,
		Title:    "macOS Docker Desktop: slow filesystem for WordPress",
		Detail: "Docker Desktop on macOS uses VirtioFS for bind mounts, " +
			"which is much slower than native ext4 for WordPress's many-small-files workload.",
		Remediation: "Consider switching to OrbStack or Colima for faster bind-mount performance.",
		HelpURL:     "https://docs.locorum.dev/macos-performance",
	}}, nil
}
