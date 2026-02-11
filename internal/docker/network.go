package docker

import (
	"fmt"
	"log/slog"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// RemoveNetworks removes Docker networks with names matching the given prefix.
func (d *Docker) RemoveNetworks(prefix string) error {
	filterArgs := filters.NewArgs()
	filterArgs.Add("name", prefix)

	networks, err := d.cli.NetworkList(d.ctx, network.ListOptions{Filters: filterArgs})
	if err != nil {
		return err
	}

	for _, n := range networks {
		slog.Info("Removing network: " + n.Name)
		if err := d.cli.NetworkRemove(d.ctx, n.ID); err != nil {
			return err
		}
	}

	return nil
}

// networkExists checks if a Docker network with the specified name exists.
func (d *Docker) networkExists(networkName string) (bool, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("name", networkName)

	networks, err := d.cli.NetworkList(d.ctx, network.ListOptions{Filters: filterArgs})
	if err != nil {
		return false, err
	}

	return len(networks) > 0, nil
}

// createNetwork creates a Docker network with the given name.
func (d *Docker) createNetwork(name string, internal bool) error {
	_, err := d.cli.NetworkInspect(d.ctx, name, network.InspectOptions{})
	if err == nil {
		// Exists.
		return nil
	}

	_, err = d.cli.NetworkCreate(d.ctx, name, network.CreateOptions{
		Driver:   "bridge",
		Internal: internal,
	})
	if err != nil {
		return fmt.Errorf("creating network %q failed: %w", name, err)
	}

	return nil
}
