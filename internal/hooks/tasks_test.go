package hooks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func TestHookValidate(t *testing.T) {
	cases := []struct {
		name   string
		hook   Hook
		errIs  error
		errSub string
	}{
		{
			name: "valid host",
			hook: Hook{TaskType: TaskExecHost, Command: "echo", Event: PostStart, Enabled: true},
		},
		{
			name: "valid exec",
			hook: Hook{TaskType: TaskExec, Command: "echo", Event: PostStart, Service: "php"},
		},
		{
			name: "valid wp-cli",
			hook: Hook{TaskType: TaskWPCLI, Command: "option get siteurl", Event: PostStart},
		},
		{
			name:  "empty command",
			hook:  Hook{TaskType: TaskExecHost, Command: "", Event: PostStart},
			errIs: ErrEmptyCommand,
		},
		{
			name:  "unknown task",
			hook:  Hook{TaskType: "ufo", Command: "x", Event: PostStart},
			errIs: ErrHookInvalid,
		},
		{
			name:   "exec on pre-start",
			hook:   Hook{TaskType: TaskExec, Command: "x", Event: PreStart},
			errIs:  ErrHookInvalid,
			errSub: "pre-start",
		},
		{
			name:   "wp-cli on pre-start",
			hook:   Hook{TaskType: TaskWPCLI, Command: "x", Event: PreStart},
			errIs:  ErrHookInvalid,
			errSub: "pre-start",
		},
		{
			name:  "service on host task",
			hook:  Hook{TaskType: TaskExecHost, Command: "x", Event: PostStart, Service: "php"},
			errIs: ErrHookInvalid,
		},
		{
			name:  "user on host task",
			hook:  Hook{TaskType: TaskExecHost, Command: "x", Event: PostStart, RunAsUser: "root"},
			errIs: ErrHookInvalid,
		},
		{
			name:  "unknown service",
			hook:  Hook{TaskType: TaskExec, Command: "x", Event: PostStart, Service: "carrot"},
			errIs: ErrHookInvalid,
		},
		{
			name:  "unknown event",
			hook:  Hook{TaskType: TaskExecHost, Command: "x", Event: "what"},
			errIs: ErrHookInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.hook.Validate()
			if tc.errIs == nil {
				if err != nil {
					t.Errorf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.errIs) {
				t.Errorf("err = %v, want %v", err, tc.errIs)
			}
			if tc.errSub != "" && err != nil {
				if !contains(err.Error(), tc.errSub) {
					t.Errorf("err = %q, missing substring %q", err.Error(), tc.errSub)
				}
			}
		})
	}
}

func TestTaskFromHook_DispatchesByType(t *testing.T) {
	site := &types.Site{Slug: "demo", FilesDir: "/x"}

	t.Run("exec defaults service to php", func(t *testing.T) {
		got, err := taskFromHook(Hook{TaskType: TaskExec, Command: "ls", Event: PostStart}, site, nil, stubContainer{}, stubHost{})
		if err != nil {
			t.Fatal(err)
		}
		ex, ok := got.(*execTask)
		if !ok {
			t.Fatalf("type = %T, want *execTask", got)
		}
		if ex.containerName != "locorum-demo-php" {
			t.Errorf("container = %q", ex.containerName)
		}
		if ex.service != "php" {
			t.Errorf("service = %q", ex.service)
		}
	})

	t.Run("exec respects service field", func(t *testing.T) {
		got, _ := taskFromHook(Hook{TaskType: TaskExec, Command: "ls", Service: "web", Event: PostStart}, site, nil, stubContainer{}, stubHost{})
		ex := got.(*execTask)
		if ex.containerName != "locorum-demo-web" {
			t.Errorf("container = %q", ex.containerName)
		}
	})

	t.Run("wp-cli prepends wp", func(t *testing.T) {
		got, err := taskFromHook(Hook{TaskType: TaskWPCLI, Command: "plugin list", Event: PostStart}, site, nil, stubContainer{}, stubHost{})
		if err != nil {
			t.Fatal(err)
		}
		ex := got.(*execTask)
		if ex.containerName != "locorum-demo-php" {
			t.Errorf("container = %q", ex.containerName)
		}
		// shell -c "wp plugin list"
		want := "wp plugin list"
		if !contains(ex.cmd[len(ex.cmd)-1], want) {
			t.Errorf("cmd[-1] = %q, want substring %q", ex.cmd[len(ex.cmd)-1], want)
		}
	})

	t.Run("exec-host uses site files dir", func(t *testing.T) {
		got, _ := taskFromHook(Hook{TaskType: TaskExecHost, Command: "x", Event: PostStart}, site, nil, stubContainer{}, stubHost{})
		ht, ok := got.(*hostTask)
		if !ok {
			t.Fatalf("type = %T, want *hostTask", got)
		}
		if ht.cwd != "/x" {
			t.Errorf("cwd = %q, want /x", ht.cwd)
		}
	})

	t.Run("rejects nil site", func(t *testing.T) {
		_, err := taskFromHook(Hook{TaskType: TaskExecHost, Command: "x", Event: PostStart}, nil, nil, stubContainer{}, stubHost{})
		if err == nil {
			t.Error("expected error for nil site")
		}
	})
}

func TestEvent_AllowsContainerTasks(t *testing.T) {
	cases := []struct {
		ev      Event
		allowed bool
	}{
		{PostStart, true},
		{PreStop, true},
		{PreStart, false},
		{PostStop, false},
		{PreDelete, false},
		{PostDelete, false},
		{PreClone, true},
		{PostClone, true},
	}
	for _, tc := range cases {
		got := tc.ev.AllowsContainerTasks()
		if got != tc.allowed {
			t.Errorf("%s.AllowsContainerTasks() = %v, want %v", tc.ev, got, tc.allowed)
		}
	}
}

// ─── stubs ──────────────────────────────────────────────────────────────────

type stubContainer struct{}

func (stubContainer) ExecInContainerStream(_ context.Context, _ string, _ ContainerExecOptions, _ func(string, bool)) (int, error) {
	return 0, nil
}

type stubHost struct{}

func (stubHost) RunHostStream(_ context.Context, _ HostExecOptions, _ func(string, bool)) (int, error) {
	return 0, nil
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
