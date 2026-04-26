package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/network"
)

// EnsureNetwork creates the named network if absent. Idempotent — returns
// the existing network's ID if it's already present.
func (d *Docker) EnsureNetwork(ctx context.Context, spec NetworkSpec) (string, error) {
	if spec.Name == "" {
		return "", fmt.Errorf("network name required")
	}

	res, err := d.cli.NetworkInspect(ctx, spec.Name, network.InspectOptions{})
	if err == nil {
		return res.ID, nil
	}
	if !isNotFoundLike(err) {
		return "", fmt.Errorf("inspecting network %q: %w", spec.Name, err)
	}

	driver := spec.Driver
	if driver == "" {
		driver = "bridge"
	}

	return withRetry(ctx, "ensure network "+spec.Name, func(ctx context.Context) (string, error) {
		created, err := d.cli.NetworkCreate(ctx, spec.Name, network.CreateOptions{
			Driver:   driver,
			Internal: spec.Internal,
			Labels:   spec.Labels,
		})
		if err != nil {
			return "", fmt.Errorf("creating network %q: %w", spec.Name, err)
		}
		return created.ID, nil
	}, func(ctx context.Context, class retryClass) error {
		// On "network already exists" race: re-inspect and let the next
		// attempt either return the existing ID or recover.
		if class == retryNetworkExists {
			if res, err := d.cli.NetworkInspect(ctx, spec.Name, network.InspectOptions{}); err == nil {
				_ = res
			}
		}
		return nil
	})
}
