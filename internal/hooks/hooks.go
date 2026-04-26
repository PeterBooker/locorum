// Package hooks lets users attach commands to the lifecycle events of a
// Locorum site (start, stop, delete, clone, version-change, multisite,
// export). Each hook is one of three task types:
//
//   - exec:      runs in a per-site Docker container (default: php).
//   - exec-host: runs in a host shell (bash on Unix/WSL, cmd on Windows).
//   - wp-cli:    convenience wrapper over `wp …` in the php container.
//
// The runner is GUI-agnostic: it streams output through callbacks and writes
// a complete on-disk log per Run. The SiteManager is responsible for firing
// pre/post hooks at every lifecycle method; the UI is responsible for
// rendering output and presenting the editor.
//
// Concurrency: hooks within a single event run sequentially in position
// order. Different events on different sites may run concurrently; the
// SiteManager owns a per-site mutex so two events on the same site do not
// interleave.
package hooks

import (
	"errors"
	"time"
)

// TaskType identifies how a Hook's command is dispatched.
type TaskType string

const (
	// TaskExec runs the command inside one of the site's running containers
	// (default: php). Use Hook.Service to target web/database/redis instead.
	TaskExec TaskType = "exec"

	// TaskExecHost runs the command on the host shell. Cwd defaults to the
	// site's FilesDir.
	TaskExecHost TaskType = "exec-host"

	// TaskWPCLI runs `wp <command>` inside the php container. Equivalent to
	// TaskExec with `wp ` prepended; offered as a separate type so the GUI
	// can validate and document the wp-cli use case.
	TaskWPCLI TaskType = "wp-cli"
)

// AllTaskTypes returns the canonical, ordered list of task types.
func AllTaskTypes() []TaskType {
	return []TaskType{TaskExec, TaskExecHost, TaskWPCLI}
}

// Valid reports whether t is a known task type.
func (t TaskType) Valid() bool {
	switch t {
	case TaskExec, TaskExecHost, TaskWPCLI:
		return true
	}
	return false
}

// Hook is a user-defined command attached to a lifecycle Event.
//
// Hooks are persisted per-site in the site_hooks table; ID is the SQLite
// row id. Position controls execution order within an event (lower runs
// first; storage assigns the next free position on insert).
type Hook struct {
	ID        int64    `json:"id"`
	SiteID    string   `json:"siteId"`
	Event     Event    `json:"event"`
	Position  int      `json:"position"`
	TaskType  TaskType `json:"taskType"`
	Command   string   `json:"command"`
	Service   string   `json:"service"`   // exec only: web|php|database|redis
	RunAsUser string   `json:"runAsUser"` // exec only: e.g. "root" or "1000:1000"
	Enabled   bool     `json:"enabled"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

// Validate checks the hook's intrinsic and event-relative invariants. It is
// called both at save time (storage layer rejects invalid hooks) and at run
// time (defence-in-depth). All errors return ErrHookInvalid wrapped with a
// human-readable message so the GUI can show it directly.
func (h Hook) Validate() error {
	if !h.TaskType.Valid() {
		return wrapInvalid("unknown task type: " + string(h.TaskType))
	}
	if h.Command == "" {
		return ErrEmptyCommand
	}
	if h.Event != "" && !h.Event.Valid() {
		return wrapInvalid("unknown event: " + string(h.Event))
	}
	if h.TaskType == TaskExec {
		if h.Service != "" && !validService(h.Service) {
			return wrapInvalid("unknown service: " + h.Service)
		}
	} else {
		if h.Service != "" {
			return wrapInvalid("service is only valid for task_type=exec")
		}
		if h.RunAsUser != "" {
			return wrapInvalid("run_as_user is only valid for task_type=exec")
		}
	}
	if h.Event != "" && !h.Event.AllowsContainerTasks() {
		// pre-start, pre-import-files, etc. — containers don't exist yet.
		switch h.TaskType {
		case TaskExec, TaskWPCLI:
			return wrapInvalid("event " + string(h.Event) + " runs before containers exist; use exec-host")
		}
	}
	return nil
}

// validService reports whether s is one of the per-site service aliases.
func validService(s string) bool {
	switch s {
	case "php", "web", "database", "redis":
		return true
	}
	return false
}

// Result describes the outcome of a single task execution.
type Result struct {
	Hook       Hook
	StartedAt  time.Time
	FinishedAt time.Time
	ExitCode   int
	Err        error
	// Stderr is true if stderr lines were observed.
	StderrSeen bool
	// LinesEmitted is the total number of stdout+stderr lines streamed.
	LinesEmitted int
	// LogPath is the absolute path of the per-event log file the runner
	// writes during the Run.
	LogPath string
}

// Duration returns the elapsed run time of a result.
func (r Result) Duration() time.Duration {
	return r.FinishedAt.Sub(r.StartedAt)
}

// Succeeded reports whether the task ran to completion with exit code 0.
func (r Result) Succeeded() bool {
	return r.Err == nil && r.ExitCode == 0
}

// Summary aggregates per-Run statistics. Emitted via RunOptions.OnAllDone.
type Summary struct {
	Event     Event
	SiteID    string
	Total     int
	Succeeded int
	Failed    int
	Skipped   int
	Aborted   bool // true if fail-strict caused early termination
	Duration  time.Duration
	LogPath   string
}

// Sentinel errors. Use errors.Is to test.
var (
	// ErrHookInvalid is the umbrella sentinel for every Validate failure.
	ErrHookInvalid = errors.New("hook is invalid")

	// ErrEmptyCommand is returned by Validate when Command is blank.
	ErrEmptyCommand = errors.New("hook command is empty")

	// ErrSkipped is returned by the runner when LOCORUM_SKIP_HOOKS is set.
	// Run() returns nil but the Summary is empty. Useful for tests to detect
	// the skip behaviour explicitly.
	ErrSkipped = errors.New("hooks skipped: LOCORUM_SKIP_HOOKS=1")
)

// invalidErr wraps a context message with ErrHookInvalid so callers can
// errors.Is(ErrHookInvalid) but still surface the specific reason.
type invalidErr struct{ msg string }

func (e *invalidErr) Error() string { return e.msg }
func (e *invalidErr) Is(target error) bool {
	return target == ErrHookInvalid
}

func wrapInvalid(msg string) error {
	return &invalidErr{msg: msg}
}
