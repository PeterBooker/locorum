package applog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/PeterBooker/locorum/internal/utils"
)

// rotatingWriter wraps the truncated file writer with size-based rotation.
// It tracks bytes written since the last rotation and triggers a rotate
// when the threshold is crossed.
//
// Concurrent Write calls are serialised by mu. The size counter is atomic
// so the cheap "do we need to rotate?" check happens outside the lock for
// the steady-state case.
type rotatingWriter struct {
	mu       sync.Mutex
	f        *os.File
	inner    io.Writer // truncatedWriter wrapping f
	logPath  string
	maxBytes int64
	keep     int
	written  atomic.Int64
}

func newRotatingWriter(f *os.File, inner io.Writer, logPath string, maxBytes int64, keep int) *rotatingWriter {
	rw := &rotatingWriter{
		f:        f,
		inner:    inner,
		logPath:  logPath,
		maxBytes: maxBytes,
		keep:     keep,
	}
	if info, err := f.Stat(); err == nil {
		rw.written.Store(info.Size())
	}
	return rw
}

func (r *rotatingWriter) Write(p []byte) (int, error) {
	if r.written.Load()+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			// Non-fatal: keep writing to the existing file. A failed
			// rotate just means the file grows past the soft cap.
			fmt.Fprintf(os.Stderr, "applog: rotate failed: %v\n", err)
		}
	}
	r.mu.Lock()
	n, err := r.inner.Write(p)
	r.mu.Unlock()
	if n > 0 {
		r.written.Add(int64(n))
	}
	return n, err
}

func (r *rotatingWriter) rotate() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.f == nil {
		return errors.New("applog: rotate on closed writer")
	}
	if err := r.f.Close(); err != nil {
		return fmt.Errorf("close current: %w", err)
	}
	if err := utils.RotateIfLarge(r.logPath, 1, r.keep); err != nil {
		// Reopen so subsequent writes don't disappear; surface the error.
		f, openErr := os.OpenFile(r.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if openErr != nil {
			return errors.Join(err, openErr)
		}
		r.f = f
		r.inner = &truncatedWriter{w: f, max: MaxLineBytes}
		return err
	}
	f, err := os.OpenFile(r.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("reopen after rotate: %w", err)
	}
	r.f = f
	r.inner = &truncatedWriter{w: f, max: MaxLineBytes}
	r.written.Store(0)

	// Update the package-level handle so closeCurrent() targets the
	// active fd.
	mu.Lock()
	currentFile = f
	mu.Unlock()
	return nil
}
