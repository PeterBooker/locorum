package docker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

// ContainersByLabel lists all containers (running or stopped) whose labels
// match every entry in the given map. An empty value matches any value for
// that label key.
func (d *Docker) ContainersByLabel(ctx context.Context, match map[string]string) ([]ContainerInfo, error) {
	args := filters.NewArgs()
	for k, v := range match {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	summaries, err := d.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	out := make([]ContainerInfo, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, ContainerInfo{
			ID:     s.ID,
			Names:  s.Names,
			Image:  s.Image,
			State:  s.State,
			Status: s.Status,
			Labels: s.Labels,
		})
	}
	return out, nil
}

// RemoveContainersByLabel force-removes every container matching the given
// label set. NotFound errors during removal are tolerated — another caller
// may have removed the container concurrently.
func (d *Docker) RemoveContainersByLabel(ctx context.Context, match map[string]string) error {
	containers, err := d.ContainersByLabel(ctx, match)
	if err != nil {
		return err
	}
	for _, c := range containers {
		name := containerInfoDisplayName(c)
		slog.Info("Removing container", "name", name)
		if err := d.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("removing container %q: %w", name, err)
		}
	}
	return nil
}

// RemoveByLabel is a backward-compatible alias retained until call sites
// are migrated. New code should use RemoveContainersByLabel.
func (d *Docker) RemoveByLabel(ctx context.Context, match map[string]string) error {
	return d.RemoveContainersByLabel(ctx, match)
}

func containerInfoDisplayName(c ContainerInfo) string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}

// ContainerExists reports whether a container with the given name exists.
func (d *Docker) ContainerExists(ctx context.Context, name string) (bool, error) {
	_, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ContainerIsRunning reports whether the container exists and is running.
func (d *Docker) ContainerIsRunning(ctx context.Context, name string) (bool, error) {
	info, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return info.State != nil && info.State.Running, nil
}
