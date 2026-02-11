package docker

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
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
		slog.Error("docker is not running or not accessible: " + err.Error())
		return err
	}

	return nil
}

func (d *Docker) CreateGlobalNetwork() error {
	exists, err := d.networkExists("locorum-global")
	if err != nil {
		slog.Error("Failed to check if global network exists: " + err.Error())
	}

	if exists {
		slog.Info("Global network already exists")
		return nil
	}

	err = d.createNetwork("locorum-global", false)
	if err != nil {
		slog.Error("Failed to create global network: " + err.Error())
		return err
	}

	return nil
}

func (d *Docker) CreateGlobalWebserver(homeDir string) error {
	exists, err := d.containerExists("locorum-global-webserver")
	if err != nil {
		slog.Error("Failed to check if global container exists: " + err.Error())
	}

	if exists {
		slog.Info("Global webserver already exists")
		return nil
	}

	containerName := "locorum-global-webserver"
	imageName := "nginx:1.28"
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
			path.Join(homeDir, ".locorum", "config", "nginx", "global.conf") + ":/etc/nginx/nginx.conf:ro",
			path.Join(homeDir, ".locorum", "config", "nginx", "map.conf") + ":/etc/nginx/map.conf:ro",
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
			networkName: {},
		},
	}

	err = d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	slog.Info("Global webserver container created successfully.")
	return nil
}

func (d *Docker) CreateGlobalMailserver() error {
	exists, err := d.containerExists("locorum-global-mail")
	if err != nil {
		slog.Error("Failed to check if global mail container exists: " + err.Error())
	}

	if exists {
		slog.Info("Global mail server already exists")
		return nil
	}

	containerName := "locorum-global-mail"
	imageName := "mailhog/mailhog"
	networkName := "locorum-global"

	config := &container.Config{
		Image: imageName,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"1025/tcp": struct{}{},
			"8025/tcp": struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		Binds:        []string{},
		PortBindings: nat.PortMap{},
		NetworkMode:  container.NetworkMode(networkName),
		ExtraHosts:   []string{},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {
				Aliases: []string{"mail"},
			},
		},
	}

	err = d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	slog.Info("Global mail container created successfully.")

	return nil
}

// CreateSite creates a new site with the given name.
func (d *Docker) CreateSite(site *types.Site, homeDir string) error {
	err := d.createNetwork("locorum-"+site.Slug, true)
	if err != nil {
		slog.Error("Failed to create site network: " + err.Error())
	}

	err = d.cli.NetworkConnect(d.ctx, "locorum-"+site.Slug, "locorum-global-webserver", &network.EndpointSettings{
		Aliases: []string{"nginx"},
	})
	if err != nil {
		slog.Error("Failed to connect global webserver to new container: " + err.Error())
		return err
	}

	err = d.cli.NetworkDisconnect(d.ctx, "locorum-"+site.Slug, "locorum-global-webserver", false)
	if err != nil {
		slog.Error(fmt.Sprintf("Failed to disconnect %s from %s: %v", "locorum-global-webserver", "locorum-"+site.Slug, err))
		return err
	}

	err = d.addWebContainer(site, homeDir)
	if err != nil {
		slog.Error("Failed to add Web Server container: " + err.Error())
		return err
	}

	err = d.addPhpContainer(site, homeDir)
	if err != nil {
		slog.Error("Failed to add PHP container: " + err.Error())
		return err
	}

	err = d.addDatabaseContainer(site, homeDir)
	if err != nil {
		slog.Error("Failed to add Database container: " + err.Error())
		return err
	}

	err = d.addRedisContainer(site, homeDir)
	if err != nil {
		slog.Error("Failed to add Redis container: " + err.Error())
		return err
	}

	return nil
}

// RemoveSite removes all containers and the network for the given site.
func (d *Docker) RemoveSite(site *types.Site) error {
	networkName := "locorum-" + site.Slug

	containerNames := []string{
		"locorum-" + site.Slug + "-redis",
		"locorum-" + site.Slug + "-database",
		"locorum-" + site.Slug + "-php",
		"locorum-" + site.Slug + "-web",
	}

	timeout := 10

	for _, cname := range containerNames {
		slog.Info(fmt.Sprintf("Stopping container %s", cname))
		if err := d.cli.ContainerStop(d.ctx, cname, container.StopOptions{
			Timeout: &timeout,
		}); err != nil {
			if !errdefs.IsNotFound(err) {
				slog.Error(fmt.Sprintf("failed to stop container %s: %v", cname, err))
			}
		}

		if err := d.cli.ContainerRemove(d.ctx, cname, container.RemoveOptions{
			RemoveVolumes: false,
			Force:         true,
		}); err != nil {
			if !errdefs.IsNotFound(err) {
				slog.Error(fmt.Sprintf("failed to remove container %s: %v", cname, err))
			}
		}
	}

	// Remove the network.
	if err := d.cli.NetworkRemove(d.ctx, networkName); err != nil {
		if !errdefs.IsNotFound(err) {
			slog.Error(fmt.Sprintf("failed to remove network %s: %v", networkName, err))
			return err
		}
	}

	return nil
}

func (d *Docker) addWebContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-web"
	imageName := "nginx:1.28-alpine"
	networkName := "locorum-" + site.Slug

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
			path.Join(home, ".locorum", "config", "nginx", "sites", site.Slug+".conf") + ":/etc/nginx/nginx.conf:ro",
			path.Join(home, ".locorum", "config", "certs") + ":/etc/nginx/certs:ro",
			site.FilesDir + ":/var/www/html:ro",
		},
		PortBindings: nat.PortMap{},
		NetworkMode:  container.NetworkMode(networkName),
		ExtraHosts:   []string{},
	}

	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"locorum-global": {
				Aliases: []string{"locorum-" + site.Slug + "-web"},
			},
			networkName: {
				Aliases: []string{"web"},
			},
		},
	}

	err := d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	return nil
}

func (d *Docker) addPhpContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-php"
	imageName := "wodby/php:8.4"
	networkName := "locorum-" + site.Slug

	config := &container.Config{
		Image:        imageName,
		Tty:          true,
		WorkingDir:   "/var/www/html",
		ExposedPorts: nat.PortSet{},
		Env: []string{
			"MYSQL_HOST=database",
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=password",
			"WP_CLI_ALLOW_ROOT=true",
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "php", "php.ini") + ":/usr/local/etc/php/conf.d/zzz-php.ini",
			site.FilesDir + ":/var/www/html",
		},
		PortBindings: nat.PortMap{},
		ExtraHosts:   []string{site.Name + ".localhost:host-gateway"},
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

func (d *Docker) addDatabaseContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-database"
	imageName := "mysql:8.4"
	networkName := "locorum-" + site.Slug
	volumeName := "locorum-" + site.Slug + "-dbdata"

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

func (d *Docker) addRedisContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-redis"
	imageName := "redis:7.4-alpine"
	networkName := "locorum-" + site.Slug

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
