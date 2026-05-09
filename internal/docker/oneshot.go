package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/PeterBooker/locorum/internal/platform"
)

// OneShotMount describes one mount attached to a transient run-and-remove
// container. Either Volume or Bind must be set, never both.
type OneShotMount struct {
	Volume   string // named Docker volume
	Bind     string // host path
	Target   string // in-container target
	ReadOnly bool
}

// OneShotResult is the captured output of RunOneShotCapture.
type OneShotResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// RunOneShotCapture launches a transient container, runs cmd, captures
// stdout / stderr, and removes the container. Used by EnsureMarkerStep
// to read the volume marker before the database container boots — which
// is too late to detect engine drift.
//
// Idempotent in the sense that callers can re-invoke after a failure;
// the helper force-removes any leftover container with the same name.
func (d *Docker) RunOneShotCapture(ctx context.Context, name, image string, cmd []string, mounts []OneShotMount) (OneShotResult, error) {
	var res OneShotResult
	if image == "" {
		return res, errors.New("oneshot: image required")
	}
	if name == "" {
		return res, errors.New("oneshot: name required")
	}

	if err := d.PullImage(ctx, image, nil); err != nil {
		return res, fmt.Errorf("ensure image %q: %w", image, err)
	}

	// Force-remove any leftover with the same name from an interrupted run.
	_ = d.RemoveContainer(ctx, name)

	binds := make([]string, 0, len(mounts))
	for _, m := range mounts {
		// Volume mounts pass the name through unchanged; bind mounts go
		// via platform.DockerPath so a Windows path doesn't reach Docker
		// with backslashes the bind syntax can't tolerate.
		var src string
		switch {
		case m.Volume != "":
			src = m.Volume
		case m.Bind != "":
			src = platform.DockerPath(m.Bind)
		}
		if src == "" || m.Target == "" {
			return res, fmt.Errorf("oneshot: mount missing source or target: %+v", m)
		}
		bind := src + ":" + m.Target
		if m.ReadOnly {
			bind += ":ro"
		}
		binds = append(binds, bind)
	}

	cfg := &container.Config{
		Image:        image,
		Cmd:          strslice.StrSlice(cmd),
		Labels:       map[string]string{LabelPlatform: PlatformValue, LabelRole: "oneshot"},
		AttachStdout: true,
		AttachStderr: true,
		Tty:          false,
	}
	hostCfg := &container.HostConfig{
		AutoRemove: false, // we remove ourselves so we can grab logs first
		Binds:      binds,
	}

	resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return res, fmt.Errorf("oneshot create: %w", err)
	}
	// Strip cancellation so the cleanup runs even when ctx is already
	// done; values/tracing from the parent are preserved.
	defer func() {
		_ = d.cli.ContainerRemove(context.WithoutCancel(ctx), resp.ID, container.RemoveOptions{Force: true})
	}()

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return res, fmt.Errorf("oneshot start: %w", err)
	}

	statusCh, errCh := d.cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil && !isNotFoundLike(err) {
			return res, fmt.Errorf("oneshot wait: %w", err)
		}
	case st := <-statusCh:
		res.ExitCode = int(st.StatusCode)
	case <-ctx.Done():
		return res, ctx.Err()
	}

	logsRC, err := d.cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return res, fmt.Errorf("oneshot logs: %w", err)
	}
	defer logsRC.Close()

	var outBuf, errBuf bytes.Buffer
	if _, err := stdcopy.StdCopy(&outBuf, &errBuf, logsRC); err != nil {
		return res, fmt.Errorf("oneshot demux: %w", err)
	}
	res.Stdout = outBuf.Bytes()
	res.Stderr = errBuf.Bytes()
	return res, nil
}
