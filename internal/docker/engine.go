package docker

import (
	"context"
	"time"

	"github.com/PeterBooker/locorum/internal/version"
)

// Engine is the surface every consumer above internal/docker uses. *Docker
// implements it; tests use internal/docker/fake.Engine. New code should
// depend on Engine, not on the concrete *Docker — it keeps the test/fake
// path open and forces per-call context plumbing.
//
// Every method takes a context.Context as its first argument. There is NO
// shared "current context" on the engine; deadlines and cancellation thread
// from the caller all the way down to the daemon socket.
//
// Every method is documented as idempotent unless explicitly stated
// otherwise — calling it twice in a row must succeed.
type Engine interface {
	// EnsureContainer creates the container described by spec if absent, or
	// recreates it if the existing container's ConfigHash label differs.
	// Returns the container ID. Idempotent.
	EnsureContainer(ctx context.Context, spec ContainerSpec) (string, error)

	// StartContainer starts an existing container by name. No-op if already
	// running. Idempotent.
	StartContainer(ctx context.Context, name string) error

	// StopContainer stops a container with a grace period before SIGKILL.
	// No-op if absent or already stopped. Idempotent.
	StopContainer(ctx context.Context, name string, grace time.Duration) error

	// RemoveContainer force-removes a container by name. No-op if absent.
	// Idempotent.
	RemoveContainer(ctx context.Context, name string) error

	// EnsureNetwork creates the named bridge network if absent. Returns its ID.
	// Idempotent.
	EnsureNetwork(ctx context.Context, spec NetworkSpec) (string, error)

	// EnsureVolume creates the named volume if absent. Returns its name.
	// Idempotent.
	EnsureVolume(ctx context.Context, spec VolumeSpec) (string, error)

	// PullImage pulls the image, streaming progress to onProgress. No-op if
	// the image is already present locally. onProgress may be nil.
	// Idempotent.
	PullImage(ctx context.Context, ref string, onProgress func(PullProgress)) error

	// WaitReady blocks until the container is healthy (per its Healthcheck)
	// or timeout elapses. Returns wrapped ErrContainerNotReady on timeout
	// with the last 50 log lines appended.
	WaitReady(ctx context.Context, name string, timeout time.Duration) error

	// ContainerLogs returns the last lines of the container's combined
	// stdout+stderr, demultiplexed for human reading.
	ContainerLogs(ctx context.Context, name string, lines int) (string, error)

	// StreamContainerLogs follows a container's stdout+stderr. The caller
	// MUST cancel ctx to release the underlying SDK connection. Pass the
	// zero time.Time to start at the live tail; pass a real time to
	// resume after a reconnect. The channel closes when ctx is cancelled,
	// the container exits, or an unrecoverable read error occurs.
	StreamContainerLogs(ctx context.Context, name string, since time.Time) (<-chan LogLine, error)

	// ChownVolume runs a privileged one-shot alpine container that
	// recursively chowns every file in the volume to uid:gid. Used before
	// service start so PHP-FPM/MySQL can write into the bind without
	// running as root themselves.
	ChownVolume(ctx context.Context, volumeName string, uid, gid int) error

	// ChownPath does the same for a host path mounted into a one-shot
	// container. Used for the site's FilesDir so wp-content/uploads has
	// the right ownership before nginx/PHP touch it.
	ChownPath(ctx context.Context, hostPath string, uid, gid int) error

	// ContainersByLabel lists containers (running or stopped) whose labels
	// match every entry in the given map. An empty value matches any value
	// for that label key.
	ContainersByLabel(ctx context.Context, match map[string]string) ([]ContainerInfo, error)

	// RemoveContainersByLabel force-removes every container matching the
	// given label set. Idempotent.
	RemoveContainersByLabel(ctx context.Context, match map[string]string) error

	// NetworksByLabel lists networks whose labels match every entry.
	NetworksByLabel(ctx context.Context, match map[string]string) ([]NetworkInfo, error)

	// RemoveNetworksByLabel removes every network matching the label set.
	// Idempotent.
	RemoveNetworksByLabel(ctx context.Context, match map[string]string) error

	// ContainerExists reports whether a container with the given name exists,
	// regardless of state.
	ContainerExists(ctx context.Context, name string) (bool, error)

	// ContainerIsRunning reports whether the container exists and is in the
	// "running" state.
	ContainerIsRunning(ctx context.Context, name string) (bool, error)

	// ProviderInfo returns Docker daemon identification, cached after first
	// call. Use RefreshProviderInfo to force a re-fetch.
	ProviderInfo(ctx context.Context) (ProviderInfo, error)

	// Ping verifies the engine is reachable. Used by health checks.
	Ping(ctx context.Context) error

	// RunOneShotCapture launches a transient container, captures stdout
	// + stderr + exit code, and removes the container. Used by
	// EnsureMarkerStep to inspect a volume before the service container
	// owning it has booted.
	RunOneShotCapture(ctx context.Context, name, image string, cmd []string, mounts []OneShotMount) (OneShotResult, error)

	// DiskUsage returns a high-level summary of `docker system df`. Slow
	// (multi-second on busy hosts); callers should pass a context with
	// a deadline (~30s) and gate via a circuit breaker if they call it
	// repeatedly. Concurrent callers are coalesced via singleflight.
	DiskUsage(ctx context.Context) (DiskReport, error)
}

// PullProgress is one tick of image-pull progress. Aggregated across layers
// into bytes-downloaded / bytes-total / status. Status is human-readable
// ("Pulling fs layer", "Extracting", "Pull complete"); the engine updates
// it on every JSON message received from Docker.
type PullProgress struct {
	Image      string
	Status     string
	Current    int64
	Total      int64
	LayerCount int
}

// ContainerInfo is the package-level view of a container. Keeps callers
// from importing docker SDK types directly.
type ContainerInfo struct {
	ID     string
	Names  []string
	Image  string
	State  string
	Status string
	Labels map[string]string
}

// NetworkInfo is the package-level view of a Docker network.
type NetworkInfo struct {
	ID     string
	Name   string
	Driver string
	Labels map[string]string
}

// ProviderInfo identifies the Docker daemon Locorum is talking to. Used to
// surface platform-specific warnings (Apple Silicon Rosetta, slow VirtioFS
// on Docker Desktop macOS, rootless Podman quirks, etc.).
type ProviderInfo struct {
	Name            string // e.g. "Docker Desktop", "Colima", "OrbStack", "docker"
	OperatingSystem string
	OSType          string // "linux", "windows"
	Architecture    string
	ServerVersion   string                      // raw, as the daemon reports it
	ServerVersionP  version.DockerServerVersion // parsed view; .IsZero() if unparseable
	Rootless        bool
	IsDockerDesktop bool
	NCPU            int
	MemTotal        int64
}

// Compile-time assertion: *Docker satisfies Engine. Drift in either fails
// the build before tests run.
var _ Engine = (*Docker)(nil)
