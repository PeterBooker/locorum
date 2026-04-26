package hooks

import (
	"context"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/utils"
)

// DockerContainerExecer wraps *docker.Docker to satisfy ContainerExecer.
//
// The narrow ContainerExecer interface lets the runner stay decoupled from
// the docker package; this adapter is the only place where the two types
// meet. Move it here (rather than into internal/docker) so the docker
// package never imports hooks.
type DockerContainerExecer struct {
	D *docker.Docker
}

// ExecInContainerStream implements ContainerExecer.
func (a DockerContainerExecer) ExecInContainerStream(ctx context.Context, container string, opts ContainerExecOptions, onLine func(string, bool)) (int, error) {
	return a.D.ExecInContainerStream(ctx, container, docker.ExecOptions{
		Cmd:        opts.Cmd,
		Env:        opts.Env,
		User:       opts.User,
		WorkingDir: opts.WorkingDir,
	}, onLine)
}

// UtilsHostExecer wraps utils.RunHostStream so it satisfies HostExecer.
type UtilsHostExecer struct{}

// RunHostStream implements HostExecer.
func (UtilsHostExecer) RunHostStream(ctx context.Context, opts HostExecOptions, onLine func(string, bool)) (int, error) {
	return utils.RunHostStream(ctx, utils.HostExecOptions{
		Command: opts.Command,
		Cwd:     opts.Cwd,
		Env:     opts.Env,
	}, onLine)
}
