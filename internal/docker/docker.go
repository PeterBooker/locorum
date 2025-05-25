package docker

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
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

// CheckDockerAvailable checks if Docker is running and accessible.
func (d *Docker) CheckDockerAvailable() error {
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

	containerName := "locorum-global-webserver"
	imageName := "nginx:1.28-alpine"
	networkName := "locorum-global"

	config := &container.Config{
		Image: imageName,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"80/tcp":  struct{}{},
			"443/tcp": struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "nginx", "global.conf") + ":/etc/nginx/nginx.conf:ro",
			path.Join(home, ".locorum", "config", "nginx", "sites-enabled") + ":/etc/nginx/sites-enabled:ro",
			path.Join(home, ".locorum", "config", "certs") + ":/etc/nginx/certs:ro",
			path.Join(home, "locorum", "sites") + ":/var/www/html:ro",
		},
		PortBindings: nat.PortMap{
			"80/tcp":  {{HostIP: "0.0.0.0", HostPort: "80"}},
			"443/tcp": {{HostIP: "0.0.0.0", HostPort: "443"}},
		},
		NetworkMode: container.NetworkMode(networkName),
		ExtraHosts:  []string{},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"locorum-global": {},
			networkName:      {},
		},
	}

	err = d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
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

	err = d.cli.NetworkConnect(d.ctx, "locorum-"+siteName, "locorum-global-webserver", &network.EndpointSettings{
		Aliases: []string{"nginx"},
	})
	if err != nil {
		rt.LogError(d.ctx, "Failed to connect global webserver to new container: "+err.Error())
		return err
	}

	err = d.cli.NetworkDisconnect(d.ctx, "locorum-"+siteName, "locorum-global-webserver", false)
	if err != nil {
		rt.LogError(d.ctx, fmt.Sprintf("Failed to disconnect %s from %s: %v", "locorum-global-webserver", "locorum-"+siteName, err))
		return err
	}

	home, _ := os.UserHomeDir()

	err = d.addPhpContainer(siteName, home)
	if err != nil {
		rt.LogError(d.ctx, "Failed to add PHP container: "+err.Error())
		return err
	}

	err = d.addDatabaseContainer(siteName, home)
	if err != nil {
		rt.LogError(d.ctx, "Failed to add Database container: "+err.Error())
		return err
	}

	err = d.addRedisContainer(siteName, home)
	if err != nil {
		rt.LogError(d.ctx, "Failed to add Redis container: "+err.Error())
		return err
	}

	return nil
}

// CreateSite creates a new site with the given name.
func (d *Docker) RemoveSite(siteName string) error {
	networkName := "locorum-" + siteName

	containerNames := []string{
		"locorum-" + siteName + "-redis",
		"locorum-" + siteName + "-database",
		"locorum-" + siteName + "-php",
	}

	timeout := 10

	for _, cname := range containerNames {
		rt.LogInfo(d.ctx, fmt.Sprintf("Stopping container %s", cname))
		if err := d.cli.ContainerStop(d.ctx, cname, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			if !errdefs.IsNotFound(err) {
				rt.LogError(d.ctx,
					fmt.Sprintf("failed to stop container %s: %v", cname, err))
			}
		}

		if err := d.cli.ContainerRemove(d.ctx, cname, container.RemoveOptions{
			RemoveVolumes: false,
			Force:         true,
		}); err != nil {
			if !errdefs.IsNotFound(err) {
				rt.LogError(d.ctx,
					fmt.Sprintf("failed to remove container %s: %v", cname, err))
			}
		}
	}

	// Remove the network.
	if err := d.cli.NetworkRemove(d.ctx, networkName); err != nil {
		if !errdefs.IsNotFound(err) {
			rt.LogError(d.ctx,
				fmt.Sprintf("failed to remove network %s: %v", networkName, err))
			return err
		}
	}

	return nil
}

func (d *Docker) addPhpContainer(siteName string, home string) error {
	containerName := "locorum-" + siteName + "-php"
	imageName := "wodby/php:8.4"
	networkName := "locorum-" + siteName

	config := &container.Config{
		Image:        imageName,
		Tty:          true,
		WorkingDir:   "/var/www/html/" + siteName,
		ExposedPorts: nat.PortSet{},
		Env: []string{
			"MYSQL_HOST=database",
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=password",
			"WP_CLI_ALLOW_ROOT=true",
			"NEW_UID=1000",
			"NEW_GID=1000",
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "php", "php.ini") + ":/usr/local/etc/php/conf.d/zzz-php.ini",
			path.Join(home, "locorum", "sites", siteName) + ":/var/www/html/" + siteName,
		},
		PortBindings: nat.PortMap{},
		NetworkMode:  container.NetworkMode(networkName),
		ExtraHosts:   []string{siteName + ".localhost:host-gateway"},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"locorum-global": {},
			networkName: {
				Aliases: []string{"php"},
			},
		},
	}

	err := d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	return nil
}

func (d *Docker) addDatabaseContainer(siteName string, home string) error {
	containerName := "locorum-" + siteName + "-database"
	imageName := "mysql:8.4"
	networkName := "locorum-" + siteName
	volumeName := "locorum-" + siteName + "-dbdata"

	err := d.createVolume(volumeName)
	if err != nil {
		return err
	}

	config := &container.Config{
		Image:        imageName,
		Tty:          true,
		Cmd:          []string{"mysqld", "--innodb-flush-method=fsync"},
		ExposedPorts: nat.PortSet{},
		Env: []string{
			"MYSQL_ROOT_PASSWORD=password",
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=password",
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			volumeName + ":/var/lib/mysql",
		},
		PortBindings: nat.PortMap{},
		NetworkMode:  container.NetworkMode(networkName),
		ExtraHosts:   []string{},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"locorum-global": {},
			networkName: {
				Aliases: []string{"database"},
			},
		},
	}

	err = d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	return nil
}

func (d *Docker) addRedisContainer(siteName string, home string) error {
	containerName := "locorum-" + siteName + "-redis"
	imageName := "redis:7.4-alpine"
	networkName := "locorum-" + siteName

	config := &container.Config{
		Image:        imageName,
		Tty:          true,
		Cmd:          []string{"redis-server", "--appendonly", "yes"},
		ExposedPorts: nat.PortSet{},
	}

	hostConfig := &container.HostConfig{
		Binds:        []string{},
		PortBindings: nat.PortMap{},
		NetworkMode:  container.NetworkMode(networkName),
		ExtraHosts:   []string{},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"locorum-global": {},
			networkName: {
				Aliases: []string{"redis"},
			},
		},
	}

	err := d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	return nil
}
