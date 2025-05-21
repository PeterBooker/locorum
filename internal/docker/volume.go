package docker

import (
	"fmt"

	"github.com/docker/docker/api/types/volume"
)

func (d *Docker) createVolume(volumeName string) error {
	_, err := d.cli.VolumeCreate(d.ctx, volume.CreateOptions{
		Name: volumeName,
	})

	if err != nil {
		return fmt.Errorf("creating volume %q failed: %w", volumeName, err)
	}

	return nil
}
