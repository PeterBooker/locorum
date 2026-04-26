package docker

import (
	"context"
	"log/slog"
	"sync"

	"github.com/docker/docker/client"
)

// GlobalNetwork is the bridge network shared by the global router, mail,
// adminer, and every per-site web/php container. Backends resolve each
// other by container alias on this network.
const GlobalNetwork = "locorum-global"

// Docker is the production Engine implementation. The struct is goroutine-safe
// for concurrent use — the Docker SDK client is internally synchronised; the
// only mutable state owned by this type is the provider-info cache, guarded
// by pmu.
type Docker struct {
	cli *client.Client

	pmu   sync.RWMutex
	pinfo *ProviderInfo
}

// New constructs a new Docker engine. The client must be set via SetClient
// before any method that talks to the daemon is called — main.go does this
// once at startup.
func New() *Docker {
	return &Docker{}
}

// SetClient injects the Docker SDK client. Separated from New so the App
// layer (which owns daemon-connection lifecycle) can construct the client
// once and share it.
func (d *Docker) SetClient(cli *client.Client) {
	d.cli = cli
}

// Ping verifies the engine is reachable. Wraps client.Ping with our context
// plumbing.
func (d *Docker) Ping(ctx context.Context) error {
	_, err := d.cli.Ping(ctx)
	return err
}

// CheckDockerAvailable is a backward-compatible alias for Ping that logs
// failure. New code should call Ping directly.
func (d *Docker) CheckDockerAvailable(ctx context.Context) error {
	if err := d.Ping(ctx); err != nil {
		slog.Error("docker is not running or not accessible: " + err.Error())
		return err
	}
	return nil
}
