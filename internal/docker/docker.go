package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strconv"

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

// CreateSite creates all containers and the network for a new site.
func (d *Docker) CreateSite(site *types.Site, homeDir string) error {
	err := d.createNetwork("locorum-"+site.Slug, true)
	if err != nil {
		slog.Error("Failed to create site network: " + err.Error())
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

// SiteContainersExist checks if the containers for a site already exist.
func (d *Docker) SiteContainersExist(site *types.Site) (bool, error) {
	return d.containerExists("locorum-" + site.Slug + "-web")
}

// StartExistingSite starts all existing containers for a site.
func (d *Docker) StartExistingSite(site *types.Site) error {
	containerNames := []string{
		"locorum-" + site.Slug + "-database",
		"locorum-" + site.Slug + "-redis",
		"locorum-" + site.Slug + "-php",
		"locorum-" + site.Slug + "-web",
	}

	for _, cname := range containerNames {
		if err := d.cli.ContainerStart(d.ctx, cname, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting container %q failed: %w", cname, err)
		}
		slog.Info(fmt.Sprintf("Container %q started", cname))
	}

	return nil
}

// StopSite stops all containers for a site without removing them.
func (d *Docker) StopSite(site *types.Site) error {
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
	}

	return nil
}

// DeleteSite stops and removes all containers and the network for the given site.
// Volumes are preserved so database data persists.
func (d *Docker) DeleteSite(site *types.Site) error {
	networkName := "locorum-" + site.Slug

	containerNames := []string{
		"locorum-" + site.Slug + "-redis",
		"locorum-" + site.Slug + "-database",
		"locorum-" + site.Slug + "-php",
		"locorum-" + site.Slug + "-web",
	}

	timeout := 10

	for _, cname := range containerNames {
		slog.Info(fmt.Sprintf("Removing container %s", cname))
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
			site.FilesDir + ":/var/www/html",
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
	imageName := "wodby/php:" + site.PHPVersion
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
			"MYSQL_PASSWORD=" + site.DBPassword,
			"WP_CLI_ALLOW_ROOT=true",
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "php", "php.ini") + ":/usr/local/etc/php/conf.d/zzz-php.ini",
			site.FilesDir + ":/var/www/html",
		},
		PortBindings: nat.PortMap{},
		ExtraHosts:   []string{site.Domain + ":host-gateway"},
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
	imageName := "mysql:" + site.MySQLVersion
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
			"MYSQL_ROOT_PASSWORD=" + site.DBPassword,
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=" + site.DBPassword,
		},
	}

	hostConfig := &container.HostConfig{
		Binds: []string{
			volumeName + ":/var/lib/mysql",
			path.Join(home, ".locorum", "config", "db", "db.cnf") + ":/etc/mysql/conf.d/locorum.cnf:ro",
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
	imageName := "redis:" + site.RedisVersion + "-alpine"
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

func (d *Docker) CreateGlobalAdminer() error {
	exists, err := d.containerExists("locorum-global-adminer")
	if err != nil {
		slog.Error("Failed to check if global adminer container exists: " + err.Error())
	}

	if exists {
		slog.Info("Global adminer already exists")
		return nil
	}

	containerName := "locorum-global-adminer"
	imageName := "adminer:latest"
	networkName := "locorum-global"

	config := &container.Config{
		Image: imageName,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"8080/tcp": struct{}{},
		},
		Env: []string{
			"ADMINER_DEFAULT_SERVER=database",
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
				Aliases: []string{"adminer"},
			},
		},
	}

	err = d.createContainer(containerName, imageName, config, hostConfig, networkingConfig)
	if err != nil {
		return err
	}

	slog.Info("Global adminer container created successfully.")
	return nil
}

// ContainerLogs returns the last N lines of logs from the named container.
func (d *Docker) ContainerLogs(containerName string, lines int) (string, error) {
	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(lines),
	}

	reader, err := d.cli.ContainerLogs(d.ctx, containerName, opts)
	if err != nil {
		return "", fmt.Errorf("fetching logs for %q: %w", containerName, err)
	}
	defer reader.Close()

	// Containers use Tty: true, so output is plain text (no stdcopy demux needed).
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", fmt.Errorf("reading logs for %q: %w", containerName, err)
	}

	return buf.String(), nil
}

// ExecInContainer runs a command inside a running container and returns the output.
func (d *Docker) ExecInContainer(containerName string, cmd []string) (string, error) {
	execConfig := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
	}

	execIDResp, err := d.cli.ContainerExecCreate(d.ctx, containerName, execConfig)
	if err != nil {
		return "", fmt.Errorf("creating exec in %q: %w", containerName, err)
	}

	attachResp, err := d.cli.ContainerExecAttach(d.ctx, execIDResp.ID, container.ExecAttachOptions{
		Tty: true,
	})
	if err != nil {
		return "", fmt.Errorf("attaching to exec in %q: %w", containerName, err)
	}
	defer attachResp.Close()

	var outputBuf bytes.Buffer
	if _, err := io.Copy(&outputBuf, attachResp.Reader); err != nil {
		return "", fmt.Errorf("reading exec output from %q: %w", containerName, err)
	}

	inspectResp, err := d.cli.ContainerExecInspect(d.ctx, execIDResp.ID)
	if err != nil {
		return outputBuf.String(), fmt.Errorf("inspecting exec in %q: %w", containerName, err)
	}

	if inspectResp.ExitCode != 0 {
		return outputBuf.String(), fmt.Errorf("command exited with code %d", inspectResp.ExitCode)
	}

	return outputBuf.String(), nil
}

// ContainerIsRunning checks if a container exists and is running.
func (d *Docker) ContainerIsRunning(name string) (bool, error) {
	info, err := d.cli.ContainerInspect(d.ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return info.State.Running, nil
}

// ContainerExists checks if a Docker container with the specified name exists (exported).
func (d *Docker) ContainerExists(name string) (bool, error) {
	return d.containerExists(name)
}

// RemoveContainer force-removes a single container by name.
func (d *Docker) RemoveContainer(name string) error {
	timeout := 10
	_ = d.cli.ContainerStop(d.ctx, name, container.StopOptions{Timeout: &timeout})
	if err := d.cli.ContainerRemove(d.ctx, name, container.RemoveOptions{Force: true}); err != nil {
		if !errdefs.IsNotFound(err) {
			return err
		}
	}
	return nil
}
