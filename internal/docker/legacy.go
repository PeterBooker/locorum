package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// CreateContainer is a thin SDK passthrough used by router/traefik to build
// its single container from raw container.Config + HostConfig + NetworkingConfig.
// It pulls the image if missing and starts the container after create.
//
// New code should prefer Engine.EnsureContainer with a ContainerSpec; this
// method exists because the router's static config is hand-crafted.
func (d *Docker) CreateContainer(
	ctx context.Context,
	name, ref string,
	cfg *container.Config,
	hostCfg *container.HostConfig,
	netCfg *network.NetworkingConfig,
) error {
	if err := d.PullImage(ctx, ref, nil); err != nil {
		return err
	}
	resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err != nil {
		return fmt.Errorf("create container %q: %w", name, err)
	}
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container %q: %w", name, err)
	}
	return nil
}

// CreateNetwork ensures a Docker bridge network with the given name and
// labels exists. Idempotent. New code should prefer Engine.EnsureNetwork.
func (d *Docker) CreateNetwork(ctx context.Context, name string, internal bool, labels map[string]string) error {
	_, err := d.EnsureNetwork(ctx, NetworkSpec{
		Name:     name,
		Internal: internal,
		Labels:   labels,
	})
	return err
}

// ExecInContainer runs a one-shot command in a running container and returns
// its combined output. Errors include the exit code; non-zero exits return
// the captured output and a wrapped error.
//
// Used by SiteManager helpers (multisite convert, mysqldump for clone/export,
// wp-cli wrapper). New streaming consumers should use ExecInContainerStream.
func (d *Docker) ExecInContainer(ctx context.Context, containerName string, cmd []string) (string, error) {
	var out []byte
	exit, err := d.ExecInContainerStream(ctx, containerName, ExecOptions{Cmd: cmd}, func(line string, stderr bool) {
		_ = stderr
		out = append(out, line...)
		out = append(out, '\n')
	})
	if err != nil {
		return string(out), err
	}
	if exit != 0 {
		return string(out), fmt.Errorf("command exited with code %d", exit)
	}
	return string(out), nil
}
