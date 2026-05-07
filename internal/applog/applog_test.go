package applog

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesToFile(t *testing.T) {
	dir := t.TempDir()
	closer, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	slog.Info("hello-test", "x", 42)
	// flush by closing+reopening for the read.
	_ = closer.Close()

	body, err := os.ReadFile(filepath.Join(dir, LogFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(body), "hello-test") {
		t.Fatalf("log missing message; got %q", string(body))
	}
}

func TestSetDebugFlipsLevel(t *testing.T) {
	if IsDebug() {
		t.Fatalf("IsDebug initially true; want false")
	}
	SetDebug(true)
	t.Cleanup(func() { SetDebug(false) })
	if !IsDebug() {
		t.Fatalf("after SetDebug(true), IsDebug = false")
	}
	if levelVar.Level() != slog.LevelDebug {
		t.Fatalf("levelVar = %v, want Debug", levelVar.Level())
	}
	SetDebug(false)
	if levelVar.Level() != slog.LevelInfo {
		t.Fatalf("after SetDebug(false), levelVar = %v, want Info", levelVar.Level())
	}
}

func TestTailLinesReturnsLastN(t *testing.T) {
	dir := t.TempDir()
	closer, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	// Write 50 lines via slog.
	for i := 0; i < 50; i++ {
		slog.Info("line", "i", i)
	}

	got, err := TailLines(10)
	if err != nil {
		t.Fatalf("TailLines: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("got %d lines, want 10", len(got))
	}
	// The last line should mention i=49.
	if !strings.Contains(got[len(got)-1], "i=49") {
		t.Fatalf("last line = %q, want it to contain i=49", got[len(got)-1])
	}
}

func TestTailLinesShorterThanRequest(t *testing.T) {
	dir := t.TempDir()
	closer, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	slog.Info("only-one")

	got, err := TailLines(50)
	if err != nil {
		t.Fatalf("TailLines: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
}

func TestRotationAtThreshold(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, LogFileName)

	// Pre-seed a file at the rotation threshold so the first Write inside
	// Init triggers a rotate.
	big := make([]byte, MaxBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(logPath, big, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	closer, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("rotation .1 missing: %v", err)
	}
	// New current file should be small (just the "Init opened" record from
	// any subsequent Info call below).
	slog.Info("post-rotate")
	_ = closer.Close()
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "post-rotate") {
		t.Fatalf("post-rotate write missing; got %q", string(body))
	}
	if len(body) > 1024 {
		t.Fatalf("post-rotate file unexpectedly large: %d bytes", len(body))
	}
}

func TestTruncatedWriterCapsLongLines(t *testing.T) {
	var buf strings.Builder
	tw := &truncatedWriter{w: &buf, max: 16}
	long := strings.Repeat("y", 1024)
	n, err := tw.Write([]byte(long))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 1024 {
		t.Fatalf("Write returned %d, want 1024", n)
	}
	if !strings.HasSuffix(buf.String(), "[truncated]\n") {
		t.Fatalf("output missing truncated marker; got %q", buf.String())
	}
	if len(buf.String()) > 16+len("…[truncated]\n") {
		t.Fatalf("output longer than expected: %d bytes", len(buf.String()))
	}
}

func TestFormatTail(t *testing.T) {
	if got := FormatTail(nil); got != "" {
		t.Fatalf("FormatTail(nil) = %q, want empty", got)
	}
	got := FormatTail([]string{"a", "b"})
	if got != "a\nb\n" {
		t.Fatalf("FormatTail = %q, want %q", got, "a\nb\n")
	}
}
