package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/strslice"

	"github.com/PeterBooker/locorum/internal/platform"
)

// chownImage is the small alpine image used by the one-shot chown helper.
// Pinned by tag so reproducible across releases; pulled on first need and
// cached for subsequent runs.
const chownImage = "alpine:3"

// ChownVolume runs a one-shot privileged container that recursively
// chowns every entry in the named volume to uid:gid. Used before
// service start so PHP-FPM/MySQL can write into the bind without running
// as root themselves.
func (d *Docker) ChownVolume(ctx context.Context, volumeName string, uid, gid int) error {
	if volumeName == "" {
		return fmt.Errorf("chown volume: name required")
	}
	return d.runChown(ctx, "chown-vol-"+sanitizeRunName(volumeName), volumeName, "/target", uid, gid, false)
}

// ChownPath does the same for a host path mounted into a one-shot
// container. Used for the site's FilesDir so wp-content/uploads has the
// right ownership before nginx/PHP touch it.
func (d *Docker) ChownPath(ctx context.Context, hostPath string, uid, gid int) error {
	if hostPath == "" {
		return fmt.Errorf("chown path: host path required")
	}
	return d.runChown(ctx, "chown-path-"+sanitizeRunName(hostPath), hostPath, "/target", uid, gid, true)
}

func (d *Docker) runChown(ctx context.Context, name, source, target string, uid, gid int, bindMount bool) error {
	if err := d.PullImage(ctx, chownImage, nil); err != nil {
		return fmt.Errorf("ensure chown image: %w", err)
	}

	// Force-remove any leftover with the same name from an interrupted run.
	_ = d.RemoveContainer(ctx, name)

	cfg := &container.Config{
		Image:      chownImage,
		Cmd:        strslice.StrSlice{"chown", "-R", fmt.Sprintf("%d:%d", uid, gid), target},
		Labels:     map[string]string{LabelPlatform: PlatformValue, LabelRole: "chown-helper"},
		WorkingDir: "/",
		Tty:        false,
	}

	hostCfg := &container.HostConfig{
		AutoRemove: true,
	}
	// Normalise host-path separators for Docker on every platform.
	// platform.DockerPath is a no-op for Linux paths and named volumes —
	// volume names are not paths and pass through unchanged.
	src := source
	if bindMount {
		src = platform.DockerPath(source)
	}
	hostCfg.Binds = []string{src + ":" + target}
	// Same syntax for both branches — Docker treats "<volume-name>:<target>"
	// as a volume mount when the source is a known volume name. We could
	// also use hostCfg.Mounts but the bind syntax is identical for our needs.

	resp, err := d.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, name)
	if err != nil {
		return fmt.Errorf("create chown container: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start chown container: %w", err)
	}

	// Block until completion. The auto-remove cleans up after exit so
	// we don't poll inspect — we wait for the daemon's "container died"
	// signal.
	statusCh, errCh := d.cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			// "no such container" can race AutoRemove — accept it.
			if isNotFoundLike(err) {
				return nil
			}
			return fmt.Errorf("wait chown container: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("chown exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// sanitizeRunName produces a Docker-name-safe suffix from an arbitrary
// path or volume name. Docker container names allow [a-zA-Z0-9_.-]; we
// replace anything else with "_" and truncate to a sensible length.
func sanitizeRunName(in string) string {
	const maxLen = 48
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > maxLen {
		out = out[len(out)-maxLen:]
	}
	if out == "" || (out[0] != '_' && (out[0] < '0' || (out[0] > '9' && out[0] < 'A') || (out[0] > 'Z' && out[0] < 'a') || out[0] > 'z')) {
		// Container names must start with [a-zA-Z0-9]; prefix if needed.
		out = "x" + out
	}
	return out
}
