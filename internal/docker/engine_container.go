package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
)

// EnsureContainer creates the container described by spec if absent, or
// recreates it if the existing container's ConfigHash label differs. Returns
// the container ID. Idempotent. The hash exclusion of LabelVersion means a
// Locorum upgrade alone does not force every container to recreate.
func (d *Docker) EnsureContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	if err := validateSpec(spec); err != nil {
		return "", err
	}

	wantHash := spec.ConfigHash()

	inspect, err := d.cli.ContainerInspect(ctx, spec.Name)
	if err != nil {
		if !isNotFoundLike(err) {
			return "", fmt.Errorf("inspect container %q: %w", spec.Name, err)
		}
		// Not present — create from scratch.
		return d.createContainerFromSpec(ctx, spec, wantHash)
	}

	if inspect.Config != nil {
		if got, ok := inspect.Config.Labels[LabelConfigHash]; ok && got == wantHash {
			return inspect.ID, nil
		}
	}

	// Shape drift — remove and recreate. The retry recover hook handles
	// the corner case where a concurrent caller force-removed the same
	// container between inspect and create.
	if err := d.RemoveContainer(ctx, spec.Name); err != nil {
		return "", fmt.Errorf("remove drifted container %q: %w", spec.Name, err)
	}
	return d.createContainerFromSpec(ctx, spec, wantHash)
}

// createContainerFromSpec is the inner CREATE path. Wrapped in withRetry to
// recover from name-in-use races (a previous crash left the same name).
func (d *Docker) createContainerFromSpec(ctx context.Context, spec ContainerSpec, hash string) (string, error) {
	cfg, hostCfg, netCfg, err := buildDockerConfig(spec, hash)
	if err != nil {
		return "", err
	}

	return withRetry(ctx, "create container "+spec.Name, func(ctx context.Context) (string, error) {
		resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, spec.Name)
		if err != nil {
			return "", redactErrSpec(err, spec)
		}
		return resp.ID, nil
	}, func(ctx context.Context, class retryClass) error {
		if class == retryNameInUse {
			// A leftover container with the same name blocks the create.
			// Force-remove and let the next attempt succeed.
			_ = d.cli.ContainerRemove(ctx, spec.Name, container.RemoveOptions{Force: true})
		}
		return nil
	})
}

// StartContainer starts an existing container. No-op if already running.
func (d *Docker) StartContainer(ctx context.Context, name string) error {
	running, err := d.ContainerIsRunning(ctx, name)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	return withRetryErr(ctx, "start container "+name, func(ctx context.Context) error {
		return d.cli.ContainerStart(ctx, name, container.StartOptions{})
	}, nil)
}

// StopContainer stops a container with a grace period. No-op if absent.
func (d *Docker) StopContainer(ctx context.Context, name string, grace time.Duration) error {
	exists, err := d.ContainerExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	secs := int(grace.Seconds())
	if secs < 0 {
		secs = 0
	}
	if err := d.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &secs}); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("stop container %q: %w", name, err)
	}
	return nil
}

// RemoveContainer force-removes a container by name. No-op if absent.
func (d *Docker) RemoveContainer(ctx context.Context, name string) error {
	if err := d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("remove container %q: %w", name, err)
	}
	return nil
}

// ContainerLogs returns the last `lines` lines of the container's logs.
// The Docker logs API multiplexes stdout+stderr into a single stream when
// the container was created with Tty:false, and returns un-multiplexed
// bytes when Tty:true. We inspect the container first so we know which
// reader to use — getting this wrong produces garbage output, which then
// hides the actual reason a container exited.
func (d *Docker) ContainerLogs(ctx context.Context, name string, lines int) (string, error) {
	if lines <= 0 {
		lines = 50
	}

	info, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", fmt.Errorf("%w: container %q", ErrNotFound, name)
		}
		return "", fmt.Errorf("inspect %q: %w", name, err)
	}
	tty := info.Config != nil && info.Config.Tty

	opts := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", lines),
	}
	rc, err := d.cli.ContainerLogs(ctx, name, opts)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", fmt.Errorf("%w: container %q", ErrNotFound, name)
		}
		return "", fmt.Errorf("fetch logs %q: %w", name, err)
	}
	defer rc.Close()

	if tty {
		return readRawLogs(rc)
	}
	return readDemuxedLogs(rc)
}

// validateSpec rejects ambiguous mounts and missing required fields.
func validateSpec(spec ContainerSpec) error {
	if spec.Name == "" {
		return errors.New("container spec: name required")
	}
	if spec.Image == "" {
		return errors.New("container spec: image required")
	}
	for i, m := range spec.Mounts {
		count := 0
		if m.Bind != nil {
			count++
		}
		if m.Volume != nil {
			count++
		}
		if m.Tmpfs != nil {
			count++
		}
		if count != 1 {
			return fmt.Errorf("container spec %q: mount[%d]: %w", spec.Name, i, ErrSpecAmbiguous)
		}
	}
	return nil
}

// buildDockerConfig translates a ContainerSpec into the SDK's three-config
// triple. Defaults are applied here so spec types stay descriptive ("zero
// value = safe") without baking SDK details into them.
func buildDockerConfig(spec ContainerSpec, hash string) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	// Labels: copy + stamp hash. Never mutate the caller's map.
	labels := map[string]string{}
	for k, v := range spec.Labels {
		labels[k] = v
	}
	labels[LabelConfigHash] = hash

	// Env: regular + secrets. We log only the keys; the values land in
	// container metadata as documented.
	env := append([]string(nil), spec.Env...)
	for _, sec := range spec.EnvSecrets {
		env = append(env, sec.Key+"="+sec.Value)
	}

	// Exposed ports — ContainerSpec carries a single Ports list with both
	// "expose only" and "host-bound" entries. HostPort=="" means container-
	// only. PortBindings goes on HostConfig only for entries with HostPort.
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range spec.Ports {
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		port, err := nat.NewPort(proto, p.ContainerPort)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid port %q/%q: %w", p.ContainerPort, proto, err)
		}
		exposed[port] = struct{}{}
		if p.HostPort != "" {
			ip := p.HostIP
			if ip == "" {
				ip = "0.0.0.0"
			}
			bindings[port] = append(bindings[port], nat.PortBinding{HostIP: ip, HostPort: p.HostPort})
		}
	}

	cfg := &container.Config{
		Image:        spec.Image,
		Cmd:          strslice.StrSlice(spec.Cmd),
		Entrypoint:   strslice.StrSlice(spec.Entrypoint),
		Env:          env,
		User:         spec.User,
		WorkingDir:   spec.WorkingDir,
		Tty:          spec.Tty,
		Labels:       labels,
		ExposedPorts: exposed,
		Healthcheck:  buildHealthcheck(spec.Healthcheck),
	}

	binds, mounts := buildMounts(spec.Mounts)
	logCfg, lcOK := buildLogConfig(spec.Resources)
	resources := buildResources(spec.Resources)
	caps := buildCaps(spec.Security)
	secOpts := buildSecurityOpt(spec.Security)
	initPtr := boolPtr(spec.Init)

	hostCfg := &container.HostConfig{
		Binds:          binds,
		Mounts:         mounts,
		PortBindings:   bindings,
		ExtraHosts:     append([]string(nil), spec.ExtraHosts...),
		CapAdd:         strslice.StrSlice(caps.add),
		CapDrop:        strslice.StrSlice(caps.drop),
		SecurityOpt:    secOpts,
		ReadonlyRootfs: spec.Security.ReadOnlyRootFS,
		Init:           initPtr,
		Resources:      resources,
		RestartPolicy:  buildRestart(spec.Restart),
	}
	if lcOK {
		hostCfg.LogConfig = logCfg
	}

	if len(spec.Networks) > 0 {
		// Primary network goes on HostConfig.NetworkMode so Docker uses it
		// as the default. Additional networks are attached after start.
		hostCfg.NetworkMode = container.NetworkMode(spec.Networks[0].Network)
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{},
	}
	for _, n := range spec.Networks {
		netCfg.EndpointsConfig[n.Network] = &network.EndpointSettings{
			Aliases: append([]string(nil), n.Aliases...),
		}
	}

	return cfg, hostCfg, netCfg, nil
}

func buildHealthcheck(hc *Healthcheck) *dockerspec.HealthcheckConfig {
	if hc == nil || len(hc.Test) == 0 {
		return nil
	}
	return &dockerspec.HealthcheckConfig{
		Test:        append([]string(nil), hc.Test...),
		Interval:    hc.Interval,
		Timeout:     hc.Timeout,
		Retries:     hc.Retries,
		StartPeriod: hc.StartPeriod,
	}
}

func buildMounts(in []Mount) ([]string, []mount.Mount) {
	var binds []string
	var mounts []mount.Mount
	for _, m := range in {
		switch {
		case m.Bind != nil:
			s := m.Bind.Source + ":" + m.Bind.Target
			if m.Bind.ReadOnly {
				s += ":ro"
			}
			binds = append(binds, s)
		case m.Volume != nil:
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeVolume,
				Source:   m.Volume.Name,
				Target:   m.Volume.Target,
				ReadOnly: m.Volume.ReadOnly,
			})
		case m.Tmpfs != nil:
			mounts = append(mounts, mount.Mount{
				Type:   mount.TypeTmpfs,
				Target: m.Tmpfs.Target,
			})
		}
	}
	return binds, mounts
}

type capSet struct {
	add  []string
	drop []string
}

func buildCaps(s SecurityOptions) capSet {
	drop := s.CapDrop
	if drop == nil {
		drop = []string{"ALL"}
	}
	return capSet{add: s.CapAdd, drop: drop}
}

func buildSecurityOpt(s SecurityOptions) []string {
	out := append([]string(nil), s.SecurityOpt...)
	// NoNewPrivileges defaults to true when the field is its zero value;
	// callers that explicitly want privilege escalation must override the
	// builder.
	if s.NoNewPrivileges {
		out = append(out, "no-new-privileges=true")
	}
	return out
}

func buildLogConfig(r Resources) (container.LogConfig, bool) {
	size := r.LogMaxSize
	files := r.LogMaxFiles
	if size == "" && files == 0 {
		// Conservative default: 10m × 3 = 30MB ceiling per container.
		size = "10m"
		files = 3
	}
	if size == "" {
		size = "10m"
	}
	if files == 0 {
		files = 3
	}
	return container.LogConfig{
		Type: "json-file",
		Config: map[string]string{
			"max-size": size,
			"max-file": fmt.Sprintf("%d", files),
		},
	}, true
}

func buildResources(r Resources) container.Resources {
	res := container.Resources{
		Memory:    r.MemoryLimit,
		CPUShares: r.CPUShares,
	}
	pids := r.PidsLimit
	if pids == 0 {
		pids = 1024
	}
	if pids > 0 {
		res.PidsLimit = &pids
	}
	for _, u := range r.Ulimits {
		res.Ulimits = append(res.Ulimits, &container.Ulimit{
			Name: u.Name,
			Soft: u.Soft,
			Hard: u.Hard,
		})
	}
	return res
}

func buildRestart(p RestartPolicy) container.RestartPolicy {
	switch p {
	case RestartOnFailure:
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure}
	case RestartUnlessStopped:
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	default:
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
}

func boolPtr(b bool) *bool { return &b }

// redactErrSpec scrubs any EnvSecret values from err.Error(). Docker's error
// strings sometimes echo container config back; we replace each secret
// value with "***" before propagating.
func redactErrSpec(err error, spec ContainerSpec) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, s := range spec.EnvSecrets {
		if s.Value == "" {
			continue
		}
		if strings.Contains(msg, s.Value) {
			msg = strings.ReplaceAll(msg, s.Value, "***")
		}
	}
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}
