package docker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// NetworksByLabel lists all networks whose labels match every entry in the
// given map.
func (d *Docker) NetworksByLabel(ctx context.Context, match map[string]string) ([]NetworkInfo, error) {
	args := filters.NewArgs()
	for k, v := range match {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	summaries, err := d.cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}
	out := make([]NetworkInfo, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, NetworkInfo{
			ID:     s.ID,
			Name:   s.Name,
			Driver: s.Driver,
			Labels: s.Labels,
		})
	}
	return out, nil
}

// RemoveNetworksByLabel removes every network matching the given label set.
// NotFound errors during removal are tolerated.
func (d *Docker) RemoveNetworksByLabel(ctx context.Context, match map[string]string) error {
	networks, err := d.NetworksByLabel(ctx, match)
	if err != nil {
		return err
	}
	for _, n := range networks {
		slog.Info("Removing network", "name", n.Name)
		if err := d.cli.NetworkRemove(ctx, n.ID); err != nil {
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
// without containers attached, which then block recreate by name.
func (d *Docker) ReconcileNetworks(ctx context.Context) error {
	return d.RemoveNetworksByLabel(ctx, map[string]string{LabelPlatform: PlatformValue})
}
