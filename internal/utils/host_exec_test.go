package utils

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunHostStream_EchoStdout(t *testing.T) {
	if runtime.GOOS == "windows" && !isWSL() {
		t.Skip("test relies on bash; skipping on native Windows")
	}
	ctx := context.Background()

	var (
		mu    sync.Mutex
		lines []string
	)
	exit, err := RunHostStream(ctx, HostExecOptions{
		Command: "printf 'hello\\nworld\\n'",
	}, func(line string, stderr bool) {
		mu.Lock()
		defer mu.Unlock()
		if stderr {
			t.Errorf("unexpected stderr line: %q", line)
		}
		lines = append(lines, line)
	})

	if err != nil {
		t.Fatalf("RunHostStream() err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if got, want := strings.Join(lines, "|"), "hello|world"; got != want {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func TestRunHostStream_StderrSeparated(t *testing.T) {
	if runtime.GOOS == "windows" && !isWSL() {
		t.Skip("test relies on bash; skipping on native Windows")
	}

	var (
		mu     sync.Mutex
		stdout []string
		stderr []string
	)
	exit, err := RunHostStream(context.Background(), HostExecOptions{
		Command: "echo out; echo err >&2",
	}, func(line string, isErr bool) {
		mu.Lock()
		defer mu.Unlock()
		if isErr {
			stderr = append(stderr, line)
		} else {
			stdout = append(stdout, line)
		}
	})

	if err != nil {
		t.Fatalf("RunHostStream() err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if want := []string{"out"}; !equalSlices(stdout, want) {
		t.Errorf("stdout = %v, want %v", stdout, want)
	}
	if want := []string{"err"}; !equalSlices(stderr, want) {
		t.Errorf("stderr = %v, want %v", stderr, want)
	}
}

func TestRunHostStream_NonZeroExitNotError(t *testing.T) {
	if runtime.GOOS == "windows" && !isWSL() {
		t.Skip("test relies on bash; skipping on native Windows")
	}

	exit, err := RunHostStream(context.Background(), HostExecOptions{
		Command: "exit 7",
	}, nil)

	if err != nil {
		t.Fatalf("RunHostStream() err = %v (non-zero exit should not be an error)", err)
	}
	if exit != 7 {
		t.Errorf("exit = %d, want 7", exit)
	}
}

func TestRunHostStream_EmptyCommand(t *testing.T) {
	if _, err := RunHostStream(context.Background(), HostExecOptions{Command: "  "}, nil); err == nil {
		t.Error("expected error on blank command, got nil")
	}
}

func TestRunHostStream_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" && !isWSL() {
		t.Skip("test relies on bash; skipping on native Windows")
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		_, _ = RunHostStream(ctx, HostExecOptions{
			Command: "sleep 30",
		}, nil)
		close(done)
	}()

	// Give the process a tick to start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHostStream did not return within 5s of cancel")
	}
}

func TestRunHostStream_EnvInjected(t *testing.T) {
	if runtime.GOOS == "windows" && !isWSL() {
		t.Skip("test relies on bash; skipping on native Windows")
	}

	var (
		mu  sync.Mutex
		out []string
	)
	exit, err := RunHostStream(context.Background(), HostExecOptions{
		Command: `printf '%s\n' "$LOCORUM_TEST_VAR"`,
		Env:     []string{"LOCORUM_TEST_VAR=hello-from-test"},
	}, func(line string, isErr bool) {
		mu.Lock()
		defer mu.Unlock()
		out = append(out, line)
	})

	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if len(out) != 1 || out[0] != "hello-from-test" {
		t.Errorf("got %v, want [hello-from-test]", out)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
