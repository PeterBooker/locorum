package docker

import (
	"context"
	"os"
	"path"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	rt "github.com/wailsapp/wails/v2/pkg/runtime"
)

type Docker struct {
	cli *client.Client
	ctx context.Context
}

func New() *Docker {
	return &Docker{}
}

// SetContext sets the context for the Docker instance.
func (d *Docker) SetContext(ctx context.Context) {
	d.ctx = ctx
}

// SetClient sets the Docker client for the Docker instance.
func (d *Docker) SetClient(cli *client.Client) {
	d.cli = cli
}

// CheckDockerRunning checks if Docker is running and accessible.
func (d *Docker) CheckDockerRunning() error {
	_, err := d.cli.Ping(d.ctx)
	if err != nil {
		rt.LogError(d.ctx, "docker is not running or not accessible: "+err.Error())
		return err
	}

	return nil
}

func (d *Docker) CreateGlobalNetwork() error {
	exists, err := d.networkExists("locorum-global")
	if err != nil {
		rt.LogError(d.ctx, "Failed to check if global network exists: "+err.Error())
	}

	if exists {
		rt.LogInfo(d.ctx, "Global network already exists")
		return nil
	}

	err = d.createNetwork("locorum-global", false)
	if err != nil {
		rt.LogError(d.ctx, "Failed to create global network: "+err.Error())
		return err
	}

	return nil
}

func (d *Docker) CreateGlobalWebserver() error {
	exists, err := d.containerExists("locorum-global-webserver")
	if err != nil {
		rt.LogError(d.ctx, "Failed to check if global container exists: "+err.Error())
	}

	if exists {
		rt.LogInfo(d.ctx, "Global network already exists")
		return nil
	}

	home, _ := os.UserHomeDir()

	options := ContainerOptions{
		NetworkName:   "locorum-global",
		ImageName:     "nginx:1-alpine",
		ContainerName: "locorum-global-webserver",
		Binds: []string{
			path.Join(home, ".locorum", "config", "nginx", "global.conf") + ":/etc/nginx/nginx.conf:ro",
			path.Join(home, ".locorum", "config", "nginx", "sites-enabled") + ":/etc/nginx/sites-enabled/",
			path.Join(home, ".locorum", "config", "certs") + ":/etc/nginx/certs/",
		},
		PortBindings: nat.PortMap{
			"80/tcp":  {{HostIP: "0.0.0.0", HostPort: "80"}},
			"443/tcp": {{HostIP: "0.0.0.0", HostPort: "443"}},
		},
		ExposedPorts: nat.PortSet{
			"80/tcp":  struct{}{},
			"443/tcp": struct{}{},
		},
		ExtraHosts: []string{},
	}

	err = d.createContainer(options)
	if err != nil {
		return err
	}

	rt.LogInfo(d.ctx, "Global webserver container created successfully.")
	return nil
}

// CreateSite creates a new site with the given name.
func (d *Docker) CreateSite(siteName string) error {
	err := d.createNetwork("locorum-"+siteName, true)
	if err != nil {
		rt.LogError(d.ctx, "Failed to create site network: "+err.Error())
	}

	home, _ := os.UserHomeDir()

	options := ContainerOptions{
		NetworkName:   "locorum-" + siteName,
		ImageName:     "devilbox/php-fpm:8.2-work",
		ContainerName: "locorum-" + siteName + "-php",
		Binds: []string{
			path.Join(home, ".locorum", "config", "php", "php.ini") + ":/usr/local/etc/php/conf.d/zzz-php.ini",
			path.Join(home, "locorum", "sites", siteName) + ":/var/www/html",
		},
		PortBindings: nat.PortMap{},
		ExposedPorts: nat.PortSet{},
		ExtraHosts:   []string{},
	}

	err = d.createContainer(options)
	if err != nil {
		rt.LogError(d.ctx, "Failed to create site container: "+err.Error())
	}

	err = d.cli.NetworkConnect(d.ctx, "locorum-"+siteName, "locorum-global-webserver", &network.EndpointSettings{
		Aliases: []string{"nginx"},
	})
	if err != nil {
		rt.LogError(d.ctx, "Failed to connect global webserver to new container: "+err.Error())
	}

	return nil
}
