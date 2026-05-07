// Package applog wires the process-wide slog handler that fan-outs to
// stderr and a rolling text log under ~/.locorum/logs/locorum.log.
//
// Init is idempotent: subsequent calls swap the active sink under a mutex
// and close the previous file. The shared LevelVar lets the Settings
// "Debug Mode" toggle flip log levels at runtime without rebuilding the
// handler (a non-atomic swap would race with in-flight Log calls).
package applog

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/PeterBooker/locorum/internal/utils"
)

// LogFileName is the canonical filename inside the configured log dir.
// Tests and external callers should refer to this constant rather than
// duplicating the literal.
const LogFileName = "locorum.log"

const (
	// MaxBytes triggers rotation. 10 MiB is a generous bound for a desktop
	// app — a typical session writes well under 1 MiB of structured logs.
	MaxBytes = 10 * 1024 * 1024
	// Keep is the number of historical files (locorum.log.1 .. .Keep)
	// retained after rotation.
	Keep = 3
	// MaxLineBytes caps a single log entry. Multi-line errors with deep
	// stack traces past this cap are truncated to keep the rotate cadence
	// predictable.
	MaxLineBytes = 8 * 1024
)

// Init configures the process-wide slog handler. It opens (and creates if
// needed) <dir>/locorum.log, fan-outs every record to stderr and the file,
// and installs the result as slog.Default. The returned io.Closer flushes
// and closes the file when the process exits — main.go should defer it.
//
// On any failure to open the file, the handler is installed with stderr
// only and the error is returned alongside a nop-Closer so the caller can
// surface a warning without dropping the rest of startup.
//
// The shared LevelVar starts at slog.LevelInfo; SetDebug toggles between
// Info and Debug at runtime. Reads of slog.Default after Init see the
// new sink immediately because Default uses an atomic.Pointer internally.
func Init(dir string) (io.Closer, error) {
	mu.Lock()
	defer mu.Unlock()

	if err := utils.EnsureDir(dir); err != nil {
		installStderrOnly()
		return nopCloser{}, fmt.Errorf("applog: ensure dir: %w", err)
	}

	logPath := filepath.Join(dir, LogFileName)
	if err := utils.RotateIfLarge(logPath, MaxBytes, Keep); err != nil {
		// Non-fatal: continue with whatever shape the file is in. A
		// pathological rotate failure (e.g. ENOSPC) will surface again
		// at write time and be logged as an error to stderr.
		fmt.Fprintf(os.Stderr, "applog: rotate failed: %v\n", err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		installStderrOnly()
		return nopCloser{}, fmt.Errorf("applog: open %q: %w", logPath, err)
	}

	// Truncated-line writer caps a single entry to MaxLineBytes — protects
	// the file against an upstream bug that floods megabyte-long lines.
	// stderr is unwrapped: the user already sees those at the terminal.
	fw := &truncatedWriter{w: f, max: MaxLineBytes}
	rw := newRotatingWriter(f, fw, logPath, MaxBytes, Keep)

	multi := io.MultiWriter(os.Stderr, rw)
	handler := slog.NewTextHandler(multi, &slog.HandlerOptions{Level: levelVar})
	slog.SetDefault(slog.New(handler))

	// Close the previous file before publishing the new one so we don't
	// hold two FDs open for the typical "called once at startup" case.
	if currentFile != nil && currentFile != f {
		_ = currentFile.Close()
	}
	currentFile = f
	currentRotater = rw
	return closerFunc(closeCurrent), nil
}

// SetDebug toggles between Info (false) and Debug (true) at runtime. The
// underlying handler is never rebuilt — slog.LevelVar is the documented
// concurrency-safe knob for this.
func SetDebug(on bool) {
	if on {
		levelVar.Set(slog.LevelDebug)
	} else {
		levelVar.Set(slog.LevelInfo)
	}
}

// IsDebug reports whether the current level is Debug. Cheap; safe from
// any goroutine.
func IsDebug() bool {
	return levelVar.Level() == slog.LevelDebug
}

// LogPath returns the absolute path of the active log file. Returns ""
// before Init or if Init failed to open the file. Used by the in-app
// "Open Log Folder" / "Copy Last 200 Lines" affordances.
func LogPath() string {
	mu.Lock()
	defer mu.Unlock()
	if currentFile == nil {
		return ""
	}
	return currentFile.Name()
}

// LogDir returns the directory containing the active log file. Returns ""
// before Init.
func LogDir() string {
	p := LogPath()
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

// TailLines reads the trailing n lines of the active log file. Returns
// nil and the underlying error if the file is unavailable. n <= 0 returns
// an empty slice.
func TailLines(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	path := LogPath()
	if path == "" {
		return nil, errors.New("applog: not initialised")
	}
	return tailLines(path, n)
}

// ────────────────────────────────────────────────────────────────────────

var (
	mu             sync.Mutex
	currentFile    *os.File
	currentRotater *rotatingWriter
	levelVar       = func() *slog.LevelVar {
		v := new(slog.LevelVar)
		v.Set(slog.LevelInfo)
		return v
	}()
)

// installStderrOnly is the fallback path when the log file cannot be opened.
// Caller must hold mu.
func installStderrOnly() {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
	slog.SetDefault(slog.New(handler))
	if currentFile != nil {
		_ = currentFile.Close()
		currentFile = nil
	}
	currentRotater = nil
}

// closeCurrent flushes and closes the active log file. Safe to call when
// no file is open.
func closeCurrent() error {
	mu.Lock()
	defer mu.Unlock()
	if currentFile == nil {
		return nil
	}
	err := currentFile.Close()
	currentFile = nil
	currentRotater = nil
	return err
}

type closerFunc func() error

func (c closerFunc) Close() error { return c() }

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// truncatedWriter caps a single Write call to max bytes. Longer writes
// are silently truncated with a "…[truncated]\n" suffix. Multiple short
// writes are passed through unchanged.
type truncatedWriter struct {
	w   io.Writer
	max int
}

func (t *truncatedWriter) Write(p []byte) (int, error) {
	if len(p) <= t.max {
		return t.w.Write(p)
	}
	suffix := []byte("…[truncated]\n")
	out := make([]byte, 0, t.max+len(suffix))
	out = append(out, p[:t.max]...)
	out = append(out, suffix...)
	if _, err := t.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
