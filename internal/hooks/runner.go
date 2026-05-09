package hooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
)

// HookLister loads the persisted hooks for a (site, event). The runner does
// not write to storage; that is the GUI's job.
type HookLister interface {
	ListHooksByEvent(siteID string, ev Event) ([]Hook, error)
}

// SettingsReader reads named user settings. Used to look up the per-site /
// global "fail on hook error" toggle.
type SettingsReader interface {
	GetSetting(key string) (string, error)
}

// Runner executes hooks for a lifecycle Event. One Runner per process.
type Runner interface {
	// Run executes every enabled hook for ev attached to site, in position
	// order. Returns the first task error if fail-strict mode is on AND a
	// task fails; otherwise nil. Runner returns nil if no hooks are
	// configured or LOCORUM_SKIP_HOOKS is set.
	Run(ctx context.Context, ev Event, site *types.Site, opts RunOptions) error

	// RunOne executes a single in-memory hook (not necessarily persisted).
	// Used by the "Run now" GUI button and the editor's "Test" button.
	// Always returns the task's error verbatim; fail-strict semantics do
	// not apply to a single task.
	RunOne(ctx context.Context, h Hook, site *types.Site, opts RunOptions) (Result, error)
}

// RunOptions bundles the streaming callbacks. Every field is optional; nil
// callbacks are no-ops. Callbacks fire on the runner's goroutine — if a
// callback blocks, the runner blocks. Implementations should hand work off
// to channels or goroutines if they need to do more than copy a string.
type RunOptions struct {
	OnTaskStart func(Hook)
	OnOutput    func(line string, stderr bool)
	OnTaskDone  func(Result)
	OnAllDone   func(Summary)
}

// Config wires the runner's external dependencies. Every field is required
// in production; tests inject fakes.
type Config struct {
	Lister      HookLister
	Container   ContainerExecer
	Host        HostExecer
	Settings    SettingsReader
	LogsBaseDir string // typically ~/.locorum/hooks/runs/

	// SkipEnvVar is the environment variable name that, if set to "1",
	// short-circuits Run() with ErrSkipped. Defaults to LOCORUM_SKIP_HOOKS.
	SkipEnvVar string

	// MaxLogsPerSite caps the number of run-log files retained per site
	// after the startup sweep. Older files are removed.
	MaxLogsPerSite int
	// LogMaxAge caps the age of retained log files. Older files are removed.
	LogMaxAge time.Duration
}

// NewRunner constructs a runner from cfg. Required fields are checked; nil
// returns an explanatory error rather than panicking later.
func NewRunner(cfg Config) (Runner, error) {
	if cfg.Lister == nil {
		return nil, errors.New("hooks.NewRunner: Lister is required")
	}
	if cfg.Container == nil {
		return nil, errors.New("hooks.NewRunner: Container is required")
	}
	if cfg.Host == nil {
		return nil, errors.New("hooks.NewRunner: Host is required")
	}
	if cfg.Settings == nil {
		return nil, errors.New("hooks.NewRunner: Settings is required")
	}
	if cfg.LogsBaseDir == "" {
		return nil, errors.New("hooks.NewRunner: LogsBaseDir is required")
	}
	if cfg.SkipEnvVar == "" {
		cfg.SkipEnvVar = "LOCORUM_SKIP_HOOKS"
	}
	if cfg.MaxLogsPerSite <= 0 {
		cfg.MaxLogsPerSite = DefaultMaxLogsPerSite
	}
	if cfg.LogMaxAge <= 0 {
		cfg.LogMaxAge = DefaultLogMaxAge
	}
	return &runner{cfg: cfg}, nil
}

const (
	// DefaultMaxLogsPerSite — applied per (slug, event) family.
	DefaultMaxLogsPerSite = 50
	// DefaultLogMaxAge — applied across the entire LogsBaseDir.
	DefaultLogMaxAge = 30 * 24 * time.Hour
)

// SettingKeyFailGlobal is the storage key for the global "fail on error"
// toggle. Per-site keys are SettingKeyFailPrefix+<siteID>.
const (
	SettingKeyFailGlobal = "hooks.fail_on_error.global"
	SettingKeyFailPrefix = "hooks.fail_on_error."
)

// ─── Implementation ─────────────────────────────────────────────────────────

type runner struct {
	cfg Config
}

// Run loads, validates, and executes hooks for ev/site.
func (r *runner) Run(ctx context.Context, ev Event, site *types.Site, opts RunOptions) error {
	if site == nil {
		return errors.New("hooks.Run: nil site")
	}

	if r.skipped() {
		opts.fireAllDone(Summary{Event: ev, SiteID: site.ID})
		return nil
	}

	hooksList, err := r.cfg.Lister.ListHooksByEvent(site.ID, ev)
	if err != nil {
		return fmt.Errorf("loading hooks: %w", err)
	}
	if len(hooksList) == 0 {
		opts.fireAllDone(Summary{Event: ev, SiteID: site.ID})
		return nil
	}

	failStrict := r.failStrictFor(site.ID)

	logFile, logPath, err := r.openRunLog(site.Slug, ev)
	if err != nil {
		// Logging-write failure should NOT abort hook execution. Surface it
		// and keep going — users care more about their hooks than the audit
		// trail.
		slog.Warn("hooks: failed to open run log", "err", err.Error())
		logFile = nil
		logPath = ""
	}
	if logFile != nil {
		defer logFile.Close()
	}

	containerEnv := BuildEnv(site, ContextContainer)
	hostEnv := BuildEnv(site, ContextHost)

	summary := Summary{
		Event:   ev,
		SiteID:  site.ID,
		LogPath: logPath,
		Total:   len(hooksList),
	}
	start := time.Now()
	r.writeLogHeader(logFile, ev, site, len(hooksList))

	var firstErr error
	for _, h := range hooksList {
		select {
		case <-ctx.Done():
			summary.Skipped += summary.Total - summary.Succeeded - summary.Failed - summary.Skipped
			summary.Aborted = true
			summary.Duration = time.Since(start)
			opts.fireAllDone(summary)
			if firstErr != nil {
				return firstErr
			}
			return ctx.Err()
		default:
		}

		if !h.Enabled {
			summary.Skipped++
			r.writeLogLine(logFile, fmt.Sprintf("== SKIPPED (disabled): %s ==", h.Command))
			continue
		}

		env := containerEnv
		if h.TaskType == TaskExecHost {
			env = hostEnv
		}

		t, err := taskFromHook(h, site, env, r.cfg.Container, r.cfg.Host)
		if err != nil {
			result := Result{Hook: h, StartedAt: time.Now(), FinishedAt: time.Now(), Err: err, LogPath: logPath, ExitCode: -1}
			summary.Failed++
			r.handleFailedTask(logFile, opts, result)
			if failStrict {
				summary.Aborted = true
				summary.Duration = time.Since(start)
				opts.fireAllDone(summary)
				return err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		result := r.runTask(ctx, t, h, opts, logFile, logPath)
		if result.Succeeded() {
			summary.Succeeded++
		} else {
			summary.Failed++
			if firstErr == nil {
				firstErr = combineTaskError(result)
			}
			if failStrict {
				summary.Aborted = true
				summary.Skipped += len(hooksList) - (summary.Succeeded + summary.Failed + summary.Skipped)
				summary.Duration = time.Since(start)
				opts.fireAllDone(summary)
				return firstErr
			}
		}
	}

	summary.Duration = time.Since(start)
	r.writeLogFooter(logFile, summary)
	opts.fireAllDone(summary)

	if failStrict {
		return firstErr
	}
	return nil
}

// RunOne executes a single hook outside the persistent flow.
func (r *runner) RunOne(ctx context.Context, h Hook, site *types.Site, opts RunOptions) (Result, error) {
	if site == nil {
		return Result{}, errors.New("hooks.RunOne: nil site")
	}
	if r.skipped() {
		return Result{Hook: h, StartedAt: time.Now(), FinishedAt: time.Now(), Err: ErrSkipped}, ErrSkipped
	}

	logFile, logPath, err := r.openRunLog(site.Slug, h.Event)
	if err != nil {
		slog.Warn("hooks: failed to open run log", "err", err.Error())
		logFile = nil
		logPath = ""
	}
	if logFile != nil {
		defer logFile.Close()
	}

	envCtx := ContextContainer
	if h.TaskType == TaskExecHost {
		envCtx = ContextHost
	}
	env := BuildEnv(site, envCtx)

	t, err := taskFromHook(h, site, env, r.cfg.Container, r.cfg.Host)
	if err != nil {
		result := Result{Hook: h, StartedAt: time.Now(), FinishedAt: time.Now(), Err: err, LogPath: logPath, ExitCode: -1}
		r.handleFailedTask(logFile, opts, result)
		return result, err
	}

	r.writeLogHeader(logFile, h.Event, site, 1)
	result := r.runTask(ctx, t, h, opts, logFile, logPath)
	r.writeLogFooter(logFile, Summary{
		Event:   h.Event,
		SiteID:  site.ID,
		LogPath: logPath,
		Total:   1,
		Succeeded: func() int {
			if result.Succeeded() {
				return 1
			}
			return 0
		}(),
		Failed: func() int {
			if result.Succeeded() {
				return 0
			}
			return 1
		}(),
		Duration: result.Duration(),
	})

	if result.Err != nil {
		return result, result.Err
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("task exited with code %d", result.ExitCode)
	}
	return result, nil
}

// runTask is the inner loop shared by Run and RunOne.
func (r *runner) runTask(ctx context.Context, t task, h Hook, opts RunOptions, logFile io.Writer, logPath string) Result {
	taskCtx, cancel := withTaskTimeout(ctx)
	defer cancel()

	result := Result{Hook: h, StartedAt: time.Now(), LogPath: logPath}
	r.writeLogLine(logFile, "")
	r.writeLogLine(logFile, "== "+t.describe()+" ==")
	r.writeLogLine(logFile, "started_at="+result.StartedAt.UTC().Format(time.RFC3339Nano))

	opts.fireTaskStart(h)

	var stderrSeen bool
	var lineCount int
	emit := func(line string, isErr bool) {
		lineCount++
		if isErr {
			stderrSeen = true
		}
		r.writeLogLine(logFile, formatLogLine(line, isErr))
		opts.fireOutput(line, isErr)
	}

	exit, err := t.run(taskCtx, emit)
	result.FinishedAt = time.Now()
	result.ExitCode = exit
	result.Err = err
	result.StderrSeen = stderrSeen
	result.LinesEmitted = lineCount

	r.writeLogLine(logFile, fmt.Sprintf("finished_at=%s exit=%d duration=%s", result.FinishedAt.UTC().Format(time.RFC3339Nano), exit, result.Duration().Truncate(time.Millisecond)))
	if err != nil {
		r.writeLogLine(logFile, "error: "+err.Error())
	}

	opts.fireTaskDone(result)
	return result
}

func (r *runner) handleFailedTask(logFile io.Writer, opts RunOptions, result Result) {
	r.writeLogLine(logFile, "")
	r.writeLogLine(logFile, "== "+formatHookHeader(result.Hook)+" ==")
	r.writeLogLine(logFile, "validation error: "+result.Err.Error())
	opts.fireTaskStart(result.Hook)
	opts.fireOutput(result.Err.Error(), true)
	opts.fireTaskDone(result)
}

// ─── Settings & skip ───────────────────────────────────────────────────────

func (r *runner) skipped() bool {
	return os.Getenv(r.cfg.SkipEnvVar) == "1"
}

// failStrictFor returns true if the per-site setting (preferred) or the
// global setting is "true". Empty / unparseable values fall back to false.
func (r *runner) failStrictFor(siteID string) bool {
	if v, _ := r.cfg.Settings.GetSetting(SettingKeyFailPrefix + siteID); v != "" {
		return parseBool(v)
	}
	v, _ := r.cfg.Settings.GetSetting(SettingKeyFailGlobal)
	return parseBool(v)
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// ─── Run-log file management ───────────────────────────────────────────────

// openRunLog creates the per-event log file at
// <LogsBaseDir>/<slug>/<event>-<RFC3339Nano>.log. Slug-less hooks land in
// "_unknown" so we never write outside the configured base.
func (r *runner) openRunLog(slug string, ev Event) (*os.File, string, error) {
	if slug == "" {
		slug = "_unknown"
	}
	dir := filepath.Join(r.cfg.LogsBaseDir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	// Replace ":" so Windows accepts the filename.
	ts := strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000Z"), ":", "-")
	name := string(ev)
	if name == "" {
		name = "ad-hoc"
	}
	path := filepath.Join(dir, name+"-"+ts+".log")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

func (r *runner) writeLogHeader(w io.Writer, ev Event, site *types.Site, taskCount int) {
	if w == nil {
		return
	}
	header := fmt.Sprintf(
		"# locorum hook run\n# event=%s\n# site=%s (%s)\n# tasks=%d\n# started=%s\n",
		ev, site.Name, site.Slug, taskCount, time.Now().UTC().Format(time.RFC3339Nano),
	)
	_, _ = io.WriteString(w, header)
}

func (r *runner) writeLogFooter(w io.Writer, s Summary) {
	if w == nil {
		return
	}
	footer := fmt.Sprintf(
		"\n# summary: total=%d succeeded=%d failed=%d skipped=%d aborted=%t duration=%s\n",
		s.Total, s.Succeeded, s.Failed, s.Skipped, s.Aborted, s.Duration.Truncate(time.Millisecond),
	)
	_, _ = io.WriteString(w, footer)
}

func (r *runner) writeLogLine(w io.Writer, line string) {
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, line+"\n")
}

func formatLogLine(line string, stderr bool) string {
	prefix := "  "
	if stderr {
		prefix = "! "
	}
	return prefix + line
}

func formatHookHeader(h Hook) string {
	return fmt.Sprintf("[%s] %s", h.TaskType, truncate(h.Command, 80))
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// SweepLogs prunes old run-log files. Safe to call from startup. Errors
// from individual files are logged at warn level and do not abort the
// sweep.
func SweepLogs(baseDir string, maxAge time.Duration, maxPerSite int) error {
	if baseDir == "" {
		return nil
	}
	if _, err := os.Stat(baseDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		siteDir := filepath.Join(baseDir, e.Name())
		sweepSite(siteDir, cutoff, maxPerSite)
	}
	return nil
}

func sweepSite(dir string, cutoff time.Time, maxPerSite int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("hooks: read site dir", "dir", dir, "err", err.Error())
		return
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		files = append(files, file{name: e.Name(), mod: info.ModTime()})
	}
	if len(files) <= maxPerSite {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files[maxPerSite:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}

// combineTaskError synthesises an error from a Result whose ExitCode is
// non-zero but whose plumbing succeeded.
func combineTaskError(r Result) error {
	if r.Err != nil {
		return r.Err
	}
	return fmt.Errorf("hook %q exited with code %d", formatHookHeader(r.Hook), r.ExitCode)
}

// ─── RunOptions plumbing ────────────────────────────────────────────────────

func (o RunOptions) fireTaskStart(h Hook) { safeFire(func() { o.OnTaskStart(h) }, o.OnTaskStart) }
func (o RunOptions) fireOutput(line string, err bool) {
	safeFire(func() { o.OnOutput(line, err) }, o.OnOutput)
}
func (o RunOptions) fireTaskDone(r Result) { safeFire(func() { o.OnTaskDone(r) }, o.OnTaskDone) }
func (o RunOptions) fireAllDone(s Summary) { safeFire(func() { o.OnAllDone(s) }, o.OnAllDone) }

// safeFire calls fn only if cb is non-nil. The zero-callback case is the
// most common in tests, so this is a tiny ergonomic guard.
func safeFire(fn func(), cb any) {
	if cb == nil || isNilCallback(cb) {
		return
	}
	defer func() {
		// A user callback panic should not crash the runner.
		if rec := recover(); rec != nil {
			slog.Error("hooks: callback panicked", "panic", fmt.Sprint(rec))
		}
	}()
	fn()
}

// isNilCallback reports whether cb is a typed nil function value. Reflection
// would be cleaner; the cost-free type-switch covers our four callback
// shapes.
func isNilCallback(cb any) bool {
	switch v := cb.(type) {
	case func(Hook):
		return v == nil
	case func(string, bool):
		return v == nil
	case func(Result):
		return v == nil
	case func(Summary):
		return v == nil
	}
	return false
}

// Compile-time guard that *sync.Mutex is never embedded. Keeps the runner
// stateless aside from cfg, which is itself read-only.
var _ = (*sync.Mutex)(nil)
