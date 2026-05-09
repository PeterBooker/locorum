// Package fake provides in-memory hooks.Runner / ContainerExecer / HostExecer
// implementations for tests in other packages. These doubles record every
// call so tests can assert on call order without spinning up Docker.
package fake

import (
	"context"
	"strings"
	"sync"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

// Runner is a deterministic stand-in for hooks.Runner. Every call appends a
// formatted entry to Calls() in invocation order.
type Runner struct {
	mu    sync.Mutex
	calls []Call

	// RunErr, if non-nil, is returned from every Run call.
	RunErr error
	// RunOneErr, if non-nil, is returned from every RunOne call.
	RunOneErr error
}

// Call records a single Run / RunOne invocation.
type Call struct {
	Method string // "Run" or "RunOne"
	Event  hooks.Event
	SiteID string
}

func New() *Runner { return &Runner{} }

// Calls returns a copy of every recorded call.
func (r *Runner) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Call, len(r.calls))
	copy(out, r.calls)
	return out
}

// Run records the call and returns the configured RunErr.
func (r *Runner) Run(_ context.Context, ev hooks.Event, site *types.Site, opts hooks.RunOptions) error {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Method: "Run", Event: ev, SiteID: siteID(site)})
	r.mu.Unlock()
	if opts.OnAllDone != nil {
		opts.OnAllDone(hooks.Summary{Event: ev, SiteID: siteID(site)})
	}
	return r.RunErr
}

// RunOne records the call and returns a synthetic Result + the configured
// RunOneErr.
func (r *Runner) RunOne(_ context.Context, h hooks.Hook, site *types.Site, opts hooks.RunOptions) (hooks.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, Call{Method: "RunOne", Event: h.Event, SiteID: siteID(site)})
	r.mu.Unlock()
	res := hooks.Result{Hook: h}
	if opts.OnTaskStart != nil {
		opts.OnTaskStart(h)
	}
	if opts.OnTaskDone != nil {
		opts.OnTaskDone(res)
	}
	return res, r.RunOneErr
}

func siteID(s *types.Site) string {
	if s == nil {
		return ""
	}
	return s.ID
}

// ─── Container / Host execers ──────────────────────────────────────────────

// ContainerExecer is a deterministic ContainerExecer for tests.
type ContainerExecer struct {
	mu    sync.Mutex
	calls []ContainerCall

	// Script is consulted by command (joined with spaces). Missing entries
	// fall back to ZeroExitCode and emit StdoutLines.
	Script map[string]ContainerScript
	// Default applies when Script lacks an entry for a command.
	Default ContainerScript
}

// ContainerCall records one ContainerExecer invocation.
type ContainerCall struct {
	Container string
	Cmd       []string
	Env       []string
	User      string
	WorkDir   string
}

// ContainerScript is the planned response for a given command.
type ContainerScript struct {
	StdoutLines []string
	StderrLines []string
	ExitCode    int
	Err         error
}

// NewContainer returns a ContainerExecer that exits 0 with no output by default.
func NewContainer() *ContainerExecer { return &ContainerExecer{Script: map[string]ContainerScript{}} }

// Calls returns a copy of every recorded call.
func (c *ContainerExecer) Calls() []ContainerCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ContainerCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// ExecInContainerStream implements hooks.ContainerExecer.
func (c *ContainerExecer) ExecInContainerStream(_ context.Context, container string, opts hooks.ContainerExecOptions, onLine func(string, bool)) (int, error) {
	c.mu.Lock()
	c.calls = append(c.calls, ContainerCall{Container: container, Cmd: append([]string(nil), opts.Cmd...), Env: append([]string(nil), opts.Env...), User: opts.User, WorkDir: opts.WorkingDir})
	script, ok := c.Script[joinCmd(opts.Cmd)]
	if !ok {
		script = c.Default
	}
	c.mu.Unlock()

	for _, line := range script.StdoutLines {
		if onLine != nil {
			onLine(line, false)
		}
	}
	for _, line := range script.StderrLines {
		if onLine != nil {
			onLine(line, true)
		}
	}
	return script.ExitCode, script.Err
}

// HostExecer is a deterministic HostExecer for tests.
type HostExecer struct {
	mu    sync.Mutex
	calls []HostCall

	Script  map[string]HostScript
	Default HostScript
}

// HostCall records one HostExecer invocation.
type HostCall struct {
	Command string
	Cwd     string
	Env     []string
}

// HostScript is the planned response for a given command line.
type HostScript struct {
	StdoutLines []string
	StderrLines []string
	ExitCode    int
	Err         error
}

// NewHost returns a HostExecer that exits 0 with no output by default.
func NewHost() *HostExecer { return &HostExecer{Script: map[string]HostScript{}} }

// Calls returns a copy of every recorded call.
func (h *HostExecer) Calls() []HostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]HostCall, len(h.calls))
	copy(out, h.calls)
	return out
}

// RunHostStream implements hooks.HostExecer.
func (h *HostExecer) RunHostStream(_ context.Context, opts hooks.HostExecOptions, onLine func(string, bool)) (int, error) {
	h.mu.Lock()
	h.calls = append(h.calls, HostCall{Command: opts.Command, Cwd: opts.Cwd, Env: append([]string(nil), opts.Env...)})
	script, ok := h.Script[opts.Command]
	if !ok {
		script = h.Default
	}
	h.mu.Unlock()

	for _, line := range script.StdoutLines {
		if onLine != nil {
			onLine(line, false)
		}
	}
	for _, line := range script.StderrLines {
		if onLine != nil {
			onLine(line, true)
		}
	}
	return script.ExitCode, script.Err
}

// ─── Hook lister & settings ────────────────────────────────────────────────

// Lister is a deterministic hooks.HookLister for tests.
type Lister struct {
	mu    sync.Mutex
	Hooks map[string]map[hooks.Event][]hooks.Hook // siteID -> event -> ordered hooks
}

func NewLister() *Lister { return &Lister{Hooks: map[string]map[hooks.Event][]hooks.Hook{}} }

// Add appends h to the (site, event) bucket. Position is auto-assigned to
// preserve insertion order so tests can write hooks declaratively.
func (l *Lister) Add(siteID string, ev hooks.Event, h hooks.Hook) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Hooks[siteID] == nil {
		l.Hooks[siteID] = map[hooks.Event][]hooks.Hook{}
	}
	h.SiteID = siteID
	h.Event = ev
	h.Position = len(l.Hooks[siteID][ev])
	l.Hooks[siteID][ev] = append(l.Hooks[siteID][ev], h)
}

// ListHooksByEvent implements hooks.HookLister.
func (l *Lister) ListHooksByEvent(siteID string, ev hooks.Event) ([]hooks.Hook, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.Hooks[siteID][ev]
	out := make([]hooks.Hook, len(src))
	copy(out, src)
	return out, nil
}

// Settings is a deterministic hooks.SettingsReader for tests.
type Settings struct {
	mu     sync.Mutex
	Values map[string]string
}

func NewSettings() *Settings { return &Settings{Values: map[string]string{}} }

// Set stores a setting.
func (s *Settings) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Values[key] = value
}

// GetSetting implements hooks.SettingsReader.
func (s *Settings) GetSetting(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Values[key], nil
}

func joinCmd(cmd []string) string {
	var b strings.Builder
	for i, p := range cmd {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(p)
	}
	return b.String()
}
