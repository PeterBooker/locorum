package docker

import (
	"fmt"

	"github.com/docker/docker/api/types/container"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// StartContainer starts a stopped container by name or ID.
func (d *Docker) StartContainer(containerName string) error {
	err := d.cli.ContainerStart(d.ctx, containerName, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("starting container %q failed: %w", containerName, err)
	}

	rt.LogInfo(d.ctx, fmt.Sprintf("Container %q started successfully.", containerName))
	return nil
}

// StopContainer stops a running container by name or ID.
func (d *Docker) StopContainer(containerName string) error {
	err := d.cli.ContainerStop(d.ctx, containerName, container.StopOptions{})
	if err != nil {
		return fmt.Errorf("stopping container %q failed: %w", containerName, err)
	}

	rt.LogInfo(d.ctx, fmt.Sprintf("Container %q stopped successfully.", containerName))
	return nil
}
