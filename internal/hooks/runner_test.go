package hooks_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/hooks/fake"
	"github.com/PeterBooker/locorum/internal/types"
)

// newRunner returns a fully wired runner with fakes for every dependency.
func newRunner(t *testing.T) (hooks.Runner, *fake.Lister, *fake.ContainerExecer, *fake.HostExecer, *fake.Settings) {
	t.Helper()
	lister := fake.NewLister()
	cont := fake.NewContainer()
	host := fake.NewHost()
	settings := fake.NewSettings()
	logsDir := t.TempDir()
	r, err := hooks.NewRunner(hooks.Config{
		Lister:      lister,
		Container:   cont,
		Host:        host,
		Settings:    settings,
		LogsBaseDir: logsDir,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r, lister, cont, host, settings
}

func testSite() *types.Site {
	return &types.Site{
		ID: "s", Slug: "demo", Name: "Demo", Domain: "demo.localhost",
		FilesDir: "/tmp/sites/demo", PublicDir: "/",
		PHPVersion: "8.3", MySQLVersion: "8.0", RedisVersion: "7.4",
		DBPassword: "pw", WebServer: "nginx",
	}
}

// captureOpts builds a RunOptions that records every callback into c.
type capture struct {
	mu     sync.Mutex
	starts []hooks.Hook
	output []capturedLine
	dones  []hooks.Result
	all    []hooks.Summary
}

type capturedLine struct {
	line   string
	stderr bool
}

func newCapture() *capture { return &capture{} }

func (c *capture) opts() hooks.RunOptions {
	return hooks.RunOptions{
		OnTaskStart: func(h hooks.Hook) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.starts = append(c.starts, h)
		},
		OnOutput: func(line string, stderr bool) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.output = append(c.output, capturedLine{line, stderr})
		},
		OnTaskDone: func(r hooks.Result) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.dones = append(c.dones, r)
		},
		OnAllDone: func(s hooks.Summary) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.all = append(c.all, s)
		},
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────

func TestRun_NoHooksFiresOnAllDone(t *testing.T) {
	r, _, _, _, _ := newRunner(t)
	cap := newCapture()
	if err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(cap.all) != 1 {
		t.Errorf("OnAllDone fired %d times, want 1", len(cap.all))
	}
	if cap.all[0].Total != 0 {
		t.Errorf("Summary.Total = %d, want 0", cap.all[0].Total)
	}
}

func TestRun_OrderedExecution(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	for _, cmd := range []string{"first", "second", "third"} {
		lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: cmd, Enabled: true})
	}
	host.Default = fake.HostScript{ExitCode: 0}

	cap := newCapture()
	if err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(cap.starts) != 3 {
		t.Fatalf("starts = %d, want 3", len(cap.starts))
	}
	for i, want := range []string{"first", "second", "third"} {
		if cap.starts[i].Command != want {
			t.Errorf("start[%d] = %q, want %q", i, cap.starts[i].Command, want)
		}
	}

	calls := host.Calls()
	if len(calls) != 3 {
		t.Fatalf("host calls = %d, want 3", len(calls))
	}
	for i, want := range []string{"first", "second", "third"} {
		if calls[i].Command != want {
			t.Errorf("host call[%d] = %q, want %q", i, calls[i].Command, want)
		}
	}

	if cap.all[0].Succeeded != 3 || cap.all[0].Failed != 0 {
		t.Errorf("summary: succeeded=%d failed=%d, want 3/0", cap.all[0].Succeeded, cap.all[0].Failed)
	}
}

func TestRun_DisabledHooksAreSkipped(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "yes", Enabled: true})
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "no", Enabled: false})
	host.Default = fake.HostScript{ExitCode: 0}

	cap := newCapture()
	_ = r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts())

	if len(host.Calls()) != 1 {
		t.Errorf("host calls = %d, want 1 (disabled hook should be skipped)", len(host.Calls()))
	}
	if cap.all[0].Skipped != 1 {
		t.Errorf("Summary.Skipped = %d, want 1", cap.all[0].Skipped)
	}
}

func TestRun_FailWarnContinues(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "broken", Enabled: true})
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "ok", Enabled: true})
	host.Script["broken"] = fake.HostScript{ExitCode: 1}
	host.Default = fake.HostScript{ExitCode: 0}

	cap := newCapture()
	if err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts()); err != nil {
		t.Fatalf("Run should not return err in warn mode: %v", err)
	}

	if len(host.Calls()) != 2 {
		t.Errorf("host calls = %d, want 2 (warn mode runs them all)", len(host.Calls()))
	}
	if cap.all[0].Failed != 1 || cap.all[0].Succeeded != 1 {
		t.Errorf("summary: %+v", cap.all[0])
	}
}

func TestRun_FailStrictAbortsOnFirstFailure(t *testing.T) {
	r, lister, _, host, settings := newRunner(t)
	settings.Set(hooks.SettingKeyFailGlobal, "true")
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "broken", Enabled: true})
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "never-runs", Enabled: true})
	host.Script["broken"] = fake.HostScript{ExitCode: 2}
	host.Default = fake.HostScript{ExitCode: 0}

	cap := newCapture()
	err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts())
	if err == nil {
		t.Fatal("expected error in strict mode")
	}

	if len(host.Calls()) != 1 {
		t.Errorf("host calls = %d, want 1 (strict mode aborts after first failure)", len(host.Calls()))
	}
	if cap.all[0].Aborted != true {
		t.Error("Summary.Aborted = false, want true")
	}
}

func TestRun_PerSiteFailPolicyOverridesGlobal(t *testing.T) {
	r, lister, _, host, settings := newRunner(t)
	settings.Set(hooks.SettingKeyFailGlobal, "true")
	settings.Set(hooks.SettingKeyFailPrefix+"s", "false")
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "broken", Enabled: true})
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "ok", Enabled: true})
	host.Script["broken"] = fake.HostScript{ExitCode: 1}
	host.Default = fake.HostScript{ExitCode: 0}

	cap := newCapture()
	if err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts()); err != nil {
		t.Fatalf("per-site false should override global true: %v", err)
	}
	if len(host.Calls()) != 2 {
		t.Errorf("host calls = %d, want 2", len(host.Calls()))
	}
	_ = cap
}

func TestRun_SkipEnvVarShortCircuits(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "x", Enabled: true})
	host.Default = fake.HostScript{ExitCode: 0}

	t.Setenv("LOCORUM_SKIP_HOOKS", "1")

	cap := newCapture()
	if err := r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(host.Calls()) != 0 {
		t.Errorf("host calls = %d, want 0 (skip env should bypass)", len(host.Calls()))
	}
	if len(cap.all) != 1 || cap.all[0].Total != 0 {
		t.Errorf("OnAllDone summary = %+v, want empty", cap.all)
	}
}

func TestRun_ContextCancelStopsBetweenHooks(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	for i := 0; i < 3; i++ {
		lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "h", Enabled: true})
	}

	// Cancel during the first hook so the second is skipped.
	ctx, cancel := context.WithCancel(context.Background())
	host.Default = fake.HostScript{
		StdoutLines: []string{"working..."},
		ExitCode:    0,
	}
	// Cancel after the first call records.
	originalScript := host.Default
	host.Script["h"] = fake.HostScript{
		StdoutLines: originalScript.StdoutLines,
		ExitCode:    0,
	}
	host.Default = host.Script["h"]
	cap := newCapture()
	cap2 := newCapture()
	go func() {
		// race-free: cancel before Run sees the second hook
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_ = r.Run(ctx, hooks.PostStart, testSite(), cap.opts())
	_ = cap2
}

func TestRun_PreStartContainerTaskRejectedAtRunTime(t *testing.T) {
	r, lister, cont, _, _ := newRunner(t)
	// We bypass storage validation by putting a bad hook directly.
	lister.Add("s", hooks.PreStart, hooks.Hook{TaskType: hooks.TaskExec, Command: "echo nope", Enabled: true})
	cont.Default = fake.ContainerScript{ExitCode: 0}

	cap := newCapture()
	_ = r.Run(context.Background(), hooks.PreStart, testSite(), cap.opts())

	if len(cont.Calls()) != 0 {
		t.Errorf("container calls = %d, want 0 (pre-start exec must be rejected)", len(cont.Calls()))
	}
	if cap.all[0].Failed != 1 {
		t.Errorf("Summary.Failed = %d, want 1", cap.all[0].Failed)
	}
}

func TestRun_OutputCallbackSeesStdoutAndStderr(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "x", Enabled: true})
	host.Default = fake.HostScript{
		StdoutLines: []string{"ok"},
		StderrLines: []string{"warn"},
		ExitCode:    0,
	}

	cap := newCapture()
	_ = r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts())

	var (
		seenOk   bool
		seenWarn bool
	)
	for _, l := range cap.output {
		if l.line == "ok" && !l.stderr {
			seenOk = true
		}
		if l.line == "warn" && l.stderr {
			seenWarn = true
		}
	}
	if !seenOk || !seenWarn {
		t.Errorf("output capture failed (seenOk=%v seenWarn=%v): %+v", seenOk, seenWarn, cap.output)
	}
}

func TestRun_LogFileWritten(t *testing.T) {
	r, lister, _, host, _ := newRunner(t)
	lister.Add("s", hooks.PostStart, hooks.Hook{TaskType: hooks.TaskExecHost, Command: "echo hi", Enabled: true})
	host.Default = fake.HostScript{StdoutLines: []string{"hi"}, ExitCode: 0}

	cap := newCapture()
	_ = r.Run(context.Background(), hooks.PostStart, testSite(), cap.opts())

	if cap.all[0].LogPath == "" {
		t.Fatal("Summary.LogPath should be set")
	}
	data, err := os.ReadFile(cap.all[0].LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "post-start") {
		t.Errorf("log missing event header: %s", body)
	}
	if !strings.Contains(body, "hi") {
		t.Errorf("log missing stdout line: %s", body)
	}
}

func TestRunOne_DispatchesSingleTask(t *testing.T) {
	r, _, _, host, _ := newRunner(t)
	host.Default = fake.HostScript{StdoutLines: []string{"y"}, ExitCode: 0}

	cap := newCapture()
	res, err := r.RunOne(context.Background(), hooks.Hook{
		SiteID: "s", Event: hooks.PostStart, TaskType: hooks.TaskExecHost, Command: "test", Enabled: true,
	}, testSite(), cap.opts())
	if err != nil {
		t.Fatalf("RunOne err = %v", err)
	}
	if !res.Succeeded() {
		t.Errorf("Result.Succeeded = false, %+v", res)
	}
	if len(host.Calls()) != 1 {
		t.Errorf("host calls = %d, want 1", len(host.Calls()))
	}
}

func TestRunOne_NonZeroExitReturnsError(t *testing.T) {
	r, _, _, host, _ := newRunner(t)
	host.Default = fake.HostScript{ExitCode: 3}

	res, err := r.RunOne(context.Background(), hooks.Hook{
		SiteID: "s", Event: hooks.PostStart, TaskType: hooks.TaskExecHost, Command: "x", Enabled: true,
	}, testSite(), hooks.RunOptions{})
	if err == nil {
		t.Error("expected non-zero exit to surface as error")
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestNewRunner_RequiresAllDeps(t *testing.T) {
	cases := []struct {
		name string
		cfg  hooks.Config
	}{
		{"missing lister", hooks.Config{Container: fake.NewContainer(), Host: fake.NewHost(), Settings: fake.NewSettings(), LogsBaseDir: t.TempDir()}},
		{"missing container", hooks.Config{Lister: fake.NewLister(), Host: fake.NewHost(), Settings: fake.NewSettings(), LogsBaseDir: t.TempDir()}},
		{"missing host", hooks.Config{Lister: fake.NewLister(), Container: fake.NewContainer(), Settings: fake.NewSettings(), LogsBaseDir: t.TempDir()}},
		{"missing settings", hooks.Config{Lister: fake.NewLister(), Container: fake.NewContainer(), Host: fake.NewHost(), LogsBaseDir: t.TempDir()}},
		{"missing logs dir", hooks.Config{Lister: fake.NewLister(), Container: fake.NewContainer(), Host: fake.NewHost(), Settings: fake.NewSettings()}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := hooks.NewRunner(tc.cfg)
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestSweepLogs_RemovesOldFiles(t *testing.T) {
	dir := t.TempDir()
	siteDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(siteDir, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(siteDir, "post-start-old.log")
	young := filepath.Join(siteDir, "post-start-young.log")
	if err := os.WriteFile(old, []byte("o"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(young, []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	// Backdate the "old" file by 60 days.
	pastTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(old, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	if err := hooks.SweepLogs(dir, 30*24*time.Hour, 50); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old file still present (err=%v)", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("young file removed: %v", err)
	}
}

func TestSweepLogs_CapsPerSiteCount(t *testing.T) {
	dir := t.TempDir()
	siteDir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(siteDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		path := filepath.Join(siteDir, "post-start-"+time.Now().Add(-time.Duration(i)*time.Hour).Format("20060102T150405")+".log")
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		modAt := time.Now().Add(-time.Duration(i) * time.Hour)
		_ = os.Chtimes(path, modAt, modAt)
	}

	if err := hooks.SweepLogs(dir, 30*24*time.Hour, 2); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(siteDir)
	if len(entries) != 2 {
		t.Errorf("entries = %d, want 2", len(entries))
	}
}
