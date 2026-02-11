package docker

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

type ContainerOptions struct {
	SiteName      string
	NetworkName   string
	ImageName     string
	ContainerName string
	Cmd           []string
	Binds         []string
	PortBindings  nat.PortMap
	ExposedPorts  nat.PortSet
	ExtraHosts    []string
}

// RemoveContainers removes Docker containers with names matching the given prefix.
func (d *Docker) RemoveContainers(prefix string) error {
	filterArgs := filters.NewArgs()
	filterArgs.Add("name", prefix)

	containers, err := d.cli.ContainerList(d.ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})
	if err != nil {
		return err
	}

	for _, c := range containers {
		slog.Info("Removing container: " + c.Names[0])
		if err := d.cli.ContainerRemove(d.ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
			return err
		}
	}

	return nil
}

// containerExists checks if a Docker container with the specified name exists
func (d *Docker) containerExists(containerName string) (bool, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("name", containerName)

	containers, err := d.cli.ContainerList(d.ctx, container.ListOptions{
		Filters: filterArgs,
		All:     true,
	})
	if err != nil {
		return false, err
	}

	return len(containers) > 0, nil
}

// createContainer creates a Docker container with the given name and image.
func (d *Docker) createContainer(containerName string, imageName string, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig) error {
	out, err := d.cli.ImagePull(d.ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull failed: %w", err)
	}
	defer out.Close()

	// Wait for image pull to complete.
	_, err = io.Copy(io.Discard, out)
	if err != nil {
		return fmt.Errorf("reading image pull stream failed: %w", err)
	}

	resp, err := d.cli.ContainerCreate(
		d.ctx,
		config,
		hostConfig,
		networkingConfig,
		nil,
		containerName,
	)
	if err != nil {
		return fmt.Errorf("creating container %q failed: %w", containerName, err)
	}

	err = d.cli.ContainerStart(d.ctx, resp.ID, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("starting container %q failed: %w", containerName, err)
	}

	return nil
}
