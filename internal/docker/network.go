package docker

import (
	"fmt"
	"log/slog"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// NetworksByLabel lists all networks whose labels match every entry in the
// given map. An empty value matches any value for that label key.
func (d *Docker) NetworksByLabel(match map[string]string) ([]network.Summary, error) {
	args := filters.NewArgs()
	for k, v := range match {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	return d.cli.NetworkList(d.ctx, network.ListOptions{Filters: args})
}

// RemoveNetworksByLabel removes every network matching the given label set.
func (d *Docker) RemoveNetworksByLabel(match map[string]string) error {
	networks, err := d.NetworksByLabel(match)
	if err != nil {
		return fmt.Errorf("listing networks: %w", err)
	}

	for _, n := range networks {
		slog.Info("Removing network: " + n.Name)
		if err := d.cli.NetworkRemove(d.ctx, n.ID); err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("removing network %q: %w", n.Name, err)
		}
	}

	return nil
}

// ReconcileNetworks removes orphaned Locorum-owned networks before recreate.
// Defensive — Docker daemon restarts and crash recovery can leave networks
// without containers attached, and recreating one with the same name then
// fails with "already exists". Mirrors DDEV's RemoveNetworkDuplicates.
func (d *Docker) ReconcileNetworks() error {
	return d.RemoveNetworksByLabel(map[string]string{LabelPlatform: PlatformValue})
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

// createNetwork creates a Docker network with the given name and labels.
// Returns nil without error if a network of that name already exists.
func (d *Docker) createNetwork(name string, internal bool, labels map[string]string) error {
	_, err := d.cli.NetworkInspect(d.ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}

	_, err = d.cli.NetworkCreate(d.ctx, name, network.CreateOptions{
		Driver:   "bridge",
		Internal: internal,
		Labels:   labels,
	})
	if err != nil {
		return fmt.Errorf("creating network %q failed: %w", name, err)
	}

	return nil
}
