package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

const GlobalNetwork = "locorum-global"

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

// CreateGlobalNetwork ensures the locorum-global bridge network exists.
// Idempotent — returns nil if the network is already there.
func (d *Docker) CreateGlobalNetwork() error {
	exists, err := d.networkExists(GlobalNetwork)
	if err != nil {
		slog.Error("Failed to check if global network exists: " + err.Error())
	}
	if exists {
		return nil
	}
	labels := PlatformLabels(RoleGlobalNetwork, "", version.Version)
	if err := d.createNetwork(GlobalNetwork, false, labels); err != nil {
		return fmt.Errorf("creating global network: %w", err)
	}
	return nil
}

// CreateGlobalMailserver ensures the mailhog container is running on the
// global network. Mailhog has no host port binding — Traefik routes to it
// via its container alias.
func (d *Docker) CreateGlobalMailserver() error {
	exists, err := d.containerExists("locorum-global-mail")
	if err != nil {
		slog.Error("Failed to check if global mail container exists: " + err.Error())
	}
	if exists {
		return nil
	}

	cfg := &container.Config{
		Image: version.MailhogImage,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"1025/tcp": struct{}{},
			"8025/tcp": struct{}{},
		},
		Labels: PlatformLabels(RoleMail, "", version.Version),
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(GlobalNetwork),
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {Aliases: []string{"mail"}},
		},
	}
	if err := d.createContainer("locorum-global-mail", version.MailhogImage, cfg, hostCfg, netCfg); err != nil {
		return err
	}
	return nil
}

// CreateGlobalAdminer ensures the adminer container is running on the
// global network. Traefik routes to it via its container alias.
func (d *Docker) CreateGlobalAdminer() error {
	exists, err := d.containerExists("locorum-global-adminer")
	if err != nil {
		slog.Error("Failed to check if global adminer container exists: " + err.Error())
	}
	if exists {
		return nil
	}

	cfg := &container.Config{
		Image: version.AdminerImage,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"8080/tcp": struct{}{},
		},
		Env: []string{
			"ADMINER_DEFAULT_SERVER=database",
		},
		Labels: PlatformLabels(RoleAdminer, "", version.Version),
	}
	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(GlobalNetwork),
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {Aliases: []string{"adminer"}},
		},
	}
	if err := d.createContainer("locorum-global-adminer", version.AdminerImage, cfg, hostCfg, netCfg); err != nil {
		return err
	}
	return nil
}

// CreateSite creates the per-site network and all four backend containers
// for the given site. Volumes are labeled too so cleanup catches them.
func (d *Docker) CreateSite(site *types.Site, homeDir string) error {
	netLabels := PlatformLabels(RoleSiteNetwork, site.Slug, version.Version)
	if err := d.createNetwork("locorum-"+site.Slug, true, netLabels); err != nil {
		return fmt.Errorf("creating site network: %w", err)
	}

	if err := d.addWebContainer(site, homeDir); err != nil {
		return fmt.Errorf("adding web container: %w", err)
	}
	if err := d.addPhpContainer(site, homeDir); err != nil {
		return fmt.Errorf("adding php container: %w", err)
	}
	if err := d.addDatabaseContainer(site, homeDir); err != nil {
		return fmt.Errorf("adding database container: %w", err)
	}
	if err := d.addRedisContainer(site, homeDir); err != nil {
		return fmt.Errorf("adding redis container: %w", err)
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
		if err := d.cli.ContainerStop(d.ctx, cname, container.StopOptions{Timeout: &timeout}); err != nil {
			if !errdefs.IsNotFound(err) {
				slog.Error(fmt.Sprintf("failed to stop container %s: %v", cname, err))
			}
		}
	}
	return nil
}

// DeleteSite stops and removes all containers and the network for the given
// site. Volumes are preserved so database data persists across delete.
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
		if err := d.cli.ContainerStop(d.ctx, cname, container.StopOptions{Timeout: &timeout}); err != nil {
			if !errdefs.IsNotFound(err) {
				slog.Error(fmt.Sprintf("failed to stop container %s: %v", cname, err))
			}
		}
		if err := d.cli.ContainerRemove(d.ctx, cname, container.RemoveOptions{Force: true}); err != nil {
			if !errdefs.IsNotFound(err) {
				slog.Error(fmt.Sprintf("failed to remove container %s: %v", cname, err))
			}
		}
	}
	if err := d.cli.NetworkRemove(d.ctx, networkName); err != nil {
		if !errdefs.IsNotFound(err) {
			slog.Error(fmt.Sprintf("failed to remove network %s: %v", networkName, err))
			return err
		}
	}
	return nil
}

func (d *Docker) addWebContainer(site *types.Site, home string) error {
	if site.WebServer == "apache" {
		return d.addApacheWebContainer(site, home)
	}
	return d.addNginxWebContainer(site, home)
}

func (d *Docker) addNginxWebContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-web"
	imageName := version.NginxImage
	networkName := "locorum-" + site.Slug

	cfg := &container.Config{
		Image: imageName,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"80/tcp": struct{}{},
		},
		Labels: PlatformLabels(RoleWeb, site.Slug, version.Version),
	}

	hostCfg := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "nginx", "sites", site.Slug+".conf") + ":/etc/nginx/nginx.conf:ro",
			site.FilesDir + ":/var/www/html",
		},
		NetworkMode: container.NetworkMode(networkName),
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {
				Aliases: []string{"locorum-" + site.Slug + "-web"},
			},
			networkName: {
				Aliases: []string{"web"},
			},
		},
	}

	return d.createContainer(containerName, imageName, cfg, hostCfg, netCfg)
}

func (d *Docker) addApacheWebContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-web"
	imageName := version.ApacheImage
	networkName := "locorum-" + site.Slug

	cfg := &container.Config{
		Image: imageName,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			"80/tcp": struct{}{},
		},
		Labels: PlatformLabels(RoleWeb, site.Slug, version.Version),
	}

	hostCfg := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "apache", "sites", site.Slug+".conf") + ":/usr/local/apache2/conf/httpd.conf:ro",
			site.FilesDir + ":/var/www/html",
		},
		NetworkMode: container.NetworkMode(networkName),
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {
				Aliases: []string{"locorum-" + site.Slug + "-web"},
			},
			networkName: {
				Aliases: []string{"web"},
			},
		},
	}

	return d.createContainer(containerName, imageName, cfg, hostCfg, netCfg)
}

func (d *Docker) addPhpContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-php"
	imageName := version.WodbyPHPImagePrefix + site.PHPVersion
	networkName := "locorum-" + site.Slug

	// On Windows os.Getuid()/os.Getgid() return -1; fall back to 1000:1000
	// which matches the default wodby user inside the image.
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		uid, gid = 1000, 1000
	}

	cfg := &container.Config{
		Image:      imageName,
		User:       fmt.Sprintf("%d:%d", uid, gid),
		Tty:        true,
		WorkingDir: "/var/www/html",
		Env: []string{
			"MYSQL_HOST=database",
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=" + site.DBPassword,
			"WP_CLI_ALLOW_ROOT=true",
		},
		Labels: PlatformLabels(RolePHP, site.Slug, version.Version),
	}

	hostCfg := &container.HostConfig{
		Binds: []string{
			path.Join(home, ".locorum", "config", "php", "php.ini") + ":/usr/local/etc/php/conf.d/zzz-php.ini",
			site.FilesDir + ":/var/www/html",
		},
		ExtraHosts: []string{site.Domain + ":host-gateway"},
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {},
			networkName: {
				Aliases: []string{"php"},
			},
		},
	}

	return d.createContainer(containerName, imageName, cfg, hostCfg, netCfg)
}

func (d *Docker) addDatabaseContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-database"
	imageName := version.MySQLImagePrefix + site.MySQLVersion
	networkName := "locorum-" + site.Slug
	volumeName := "locorum-" + site.Slug + "-dbdata"

	volumeLabels := PlatformLabels(RoleDatabaseData, site.Slug, version.Version)
	if err := d.createVolume(volumeName, volumeLabels); err != nil {
		return err
	}

	cfg := &container.Config{
		Image: imageName,
		Tty:   true,
		Cmd:   []string{"mysqld", "--innodb-flush-method=fsync"},
		Env: []string{
			"MYSQL_ROOT_PASSWORD=" + site.DBPassword,
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
			"MYSQL_PASSWORD=" + site.DBPassword,
		},
		Labels: PlatformLabels(RoleDatabase, site.Slug, version.Version),
	}

	hostCfg := &container.HostConfig{
		Binds: []string{
			volumeName + ":/var/lib/mysql",
			path.Join(home, ".locorum", "config", "db", "db.cnf") + ":/etc/mysql/conf.d/locorum.cnf:ro",
		},
		NetworkMode: container.NetworkMode(networkName),
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {},
			networkName: {
				Aliases: []string{"database"},
			},
		},
	}

	return d.createContainer(containerName, imageName, cfg, hostCfg, netCfg)
}

func (d *Docker) addRedisContainer(site *types.Site, home string) error {
	containerName := "locorum-" + site.Slug + "-redis"
	imageName := version.RedisImagePrefix + site.RedisVersion + version.RedisImageSuffix
	networkName := "locorum-" + site.Slug

	cfg := &container.Config{
		Image:  imageName,
		Tty:    true,
		Cmd:    []string{"redis-server", "--appendonly", "yes"},
		Labels: PlatformLabels(RoleRedis, site.Slug, version.Version),
	}

	hostCfg := &container.HostConfig{
		NetworkMode: container.NetworkMode(networkName),
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			GlobalNetwork: {},
			networkName: {
				Aliases: []string{"redis"},
			},
		},
	}

	return d.createContainer(containerName, imageName, cfg, hostCfg, netCfg)
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

	attachResp, err := d.cli.ContainerExecAttach(d.ctx, execIDResp.ID, container.ExecAttachOptions{Tty: true})
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

// ContainerExists checks if a Docker container with the specified name exists.
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
