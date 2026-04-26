package hooks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
)

// task is the internal interface implemented by every dispatchable task
// type. Each Hook is mapped to a task by taskFromHook; the runner only
// talks to this interface.
type task interface {
	// run dispatches the command and emits stdout/stderr lines via emit.
	// Returns the command's exit code and a plumbing error (NOT a non-zero
	// exit code, which is reported via the int).
	run(ctx context.Context, emit lineEmitter) (int, error)

	// describe returns a single-line human-readable label for the task,
	// suitable for log headers ("[exec/php] wp option get siteurl").
	describe() string
}

// lineEmitter is the demuxed line callback exposed to tasks.
type lineEmitter func(line string, stderr bool)

// ContainerExecer is the narrow port the runner uses to run an exec task
// inside a Docker container. The production implementation is
// docker.Docker.ExecInContainerStream; tests inject a fake.
type ContainerExecer interface {
	ExecInContainerStream(ctx context.Context, containerName string, opts ContainerExecOptions, onLine func(string, bool)) (int, error)
}

// ContainerExecOptions mirrors docker.ExecOptions but is decoupled so the
// hooks package does not need to import docker. The runner-side glue layer
// translates between the two.
type ContainerExecOptions struct {
	Cmd        []string
	Env        []string
	User       string
	WorkingDir string
}

// HostExecer is the narrow port the runner uses to run an exec-host task on
// the host shell. The production implementation is
// utils.RunHostStream wrapped to match this signature.
type HostExecer interface {
	RunHostStream(ctx context.Context, opts HostExecOptions, onLine func(string, bool)) (int, error)
}

// HostExecOptions mirrors utils.HostExecOptions for the same reason.
type HostExecOptions struct {
	Command string
	Cwd     string
	Env     []string
}

// ─── Concrete tasks ─────────────────────────────────────────────────────────

// execTask runs a command inside one of the site's containers.
type execTask struct {
	containerName string
	service       string // "php" / "web" / "database" / "redis"
	cmd           []string
	user          string
	env           []string

	d ContainerExecer
}

func (t *execTask) run(ctx context.Context, emit lineEmitter) (int, error) {
	return t.d.ExecInContainerStream(ctx, t.containerName, ContainerExecOptions{
		Cmd:  t.cmd,
		Env:  t.env,
		User: t.user,
	}, emit)
}

func (t *execTask) describe() string {
	return "[exec/" + t.service + "] " + strings.Join(t.cmd, " ")
}

// hostTask runs a command on the host shell.
type hostTask struct {
	command string
	cwd     string
	env     []string

	h HostExecer
}

func (t *hostTask) run(ctx context.Context, emit lineEmitter) (int, error) {
	return t.h.RunHostStream(ctx, HostExecOptions{
		Command: t.command,
		Cwd:     t.cwd,
		Env:     t.env,
	}, emit)
}

func (t *hostTask) describe() string {
	return "[exec-host] " + t.command
}

// ─── Task construction ─────────────────────────────────────────────────────

// taskFromHook builds the appropriate task implementation for h. It performs
// the same validation as Hook.Validate plus event-aware checks (e.g. wp-cli
// on pre-start is rejected here even if the row was somehow saved past the
// storage-layer guard).
//
// The site and env arguments supply the per-site context: site is used for
// container naming and cwd defaults, env is the LOCORUM_* env-var bundle
// that must be injected into every task.
func taskFromHook(h Hook, site *types.Site, env []string, d ContainerExecer, host HostExecer) (task, error) {
	if site == nil {
		return nil, errors.New("taskFromHook: nil site")
	}
	if err := h.Validate(); err != nil {
		return nil, err
	}

	switch h.TaskType {
	case TaskExec:
		service := h.Service
		if service == "" {
			service = "php"
		}
		container := "locorum-" + site.Slug + "-" + service
		// Use bash -c so the user can write `cmd1 && cmd2` and have ${VAR}
		// expansion. The wodby PHP image and our nginx/apache/redis bases
		// all ship a POSIX-compatible shell; the database (mysql) image
		// ships /bin/bash. If any of these change we'd need a per-service
		// shell map; until then, /bin/sh is the safest universal fallback.
		shell := "/bin/sh"
		return &execTask{
			containerName: container,
			service:       service,
			cmd:           []string{shell, "-c", h.Command},
			user:          h.RunAsUser,
			env:           env,
			d:             d,
		}, nil

	case TaskWPCLI:
		container := "locorum-" + site.Slug + "-php"
		// `sh -c "wp <command>"` so the user can write
		// "search-replace ${LOCORUM_DOMAIN} new.localhost" and shell-time
		// expansion happens in the container.
		return &execTask{
			containerName: container,
			service:       "php",
			cmd:           []string{"/bin/sh", "-c", "wp " + h.Command},
			user:          h.RunAsUser,
			env:           env,
			d:             d,
		}, nil

	case TaskExecHost:
		return &hostTask{
			command: h.Command,
			cwd:     site.FilesDir,
			env:     env,
			h:       host,
		}, nil
	}
	return nil, fmt.Errorf("taskFromHook: unhandled task type %q", h.TaskType)
}

// ─── Default task timeout ──────────────────────────────────────────────────

// DefaultTaskTimeout is the per-task wall-clock deadline. Hooks should not
// run for longer than this; the runner cancels the task's context when it
// elapses. v1 uses a global default; the schema reserves room for a
// per-hook column.
const DefaultTaskTimeout = 5 * time.Minute

// withTaskTimeout returns a context that cancels after DefaultTaskTimeout.
// Centralised here so future per-hook timeouts can plug in cleanly.
func withTaskTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, DefaultTaskTimeout)
}
