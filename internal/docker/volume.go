package docker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
)

// volumeExists checks whether a named volume already exists.
func (d *Docker) volumeExists(ctx context.Context, name string) (bool, error) {
	_, err := d.cli.VolumeInspect(ctx, name)
	if err != nil {
		if isNotFoundLike(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// EnsureVolume creates the named volume if absent. Returns its name.
// Idempotent.
func (d *Docker) EnsureVolume(ctx context.Context, spec VolumeSpec) (string, error) {
	if spec.Name == "" {
		return "", fmt.Errorf("volume name required")
	}
	exists, err := d.volumeExists(ctx, spec.Name)
	if err != nil {
		return "", fmt.Errorf("inspecting volume %q: %w", spec.Name, err)
	}
	if exists {
		return spec.Name, nil
	}
	if _, err := d.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   spec.Name,
		Labels: spec.Labels,
	}); err != nil {
		return "", fmt.Errorf("creating volume %q: %w", spec.Name, err)
	}
	return spec.Name, nil
}

// RemoveVolumesByLabel removes every volume whose labels match the given
// set. Used by the three-way "purge" delete path. Idempotent — NotFound
// errors during removal are tolerated.
func (d *Docker) RemoveVolumesByLabel(ctx context.Context, match map[string]string) error {
	args := filters.NewArgs()
	for k, v := range match {
		if v == "" {
			args.Add("label", k)
		} else {
			args.Add("label", k+"="+v)
		}
	}
	res, err := d.cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return fmt.Errorf("listing volumes: %w", err)
	}
	for _, v := range res.Volumes {
		slog.Info("Removing volume", "name", v.Name)
		if err := d.cli.VolumeRemove(ctx, v.Name, true); err != nil {
			if errdefs.IsNotFound(err) || isNotFoundLike(err) {
				continue
			}
			return fmt.Errorf("removing volume %q: %w", v.Name, err)
		}
	}
	return nil
}
