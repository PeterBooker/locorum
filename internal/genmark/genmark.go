// Package genmark encodes the convention that Locorum-managed files
// carry a signature so the user can opt out of further regeneration by
// removing the line.
//
// Convention: every generated file under ~/.locorum/ or
// ~/locorum/sites/<slug>/ that the user might want to override starts
// with a leading comment block containing the substring "locorum-
// generated" within the first 4 KiB. WriteIfManaged respects
// user-removed signatures by refusing to overwrite a file without the
// marker; WriteAtomic writes unconditionally for files Locorum
// exclusively owns (e.g. router dynamic configs the user has no reason
// to hand-edit).
//
// The package is leaf-level: depends on stdlib only. Sits beside
// internal/storage in the dependency graph and is imported by every
// writer that emits managed files (sites/wpconfig.go, sites/files.go,
// router/traefik/, sites/configyaml/, ...).
//
// All writes go through tmp-file + rename so a crash mid-write never
// leaves a half-rendered config on disk. Byte-equal writes are
// short-circuited so idempotent regenerates do not bump mtimes or wake
// inotify watchers (Traefik's file-provider reload pipeline depends on
// this).
package genmark

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// Marker is the case-sensitive substring searched for inside the
// leading bytes of a generated file. Style-agnostic: callers prepend
// their own comment prefix via Header. The phrase does not appear in
// real PHP, YAML, or shell code, so substring matching has effectively
// no false positives.
const Marker = "locorum-generated"

// peekBytes caps the prefix scanned for Marker. 4 KiB is ample — the
// marker always lives in a file's leading comment block. Larger values
// just inflate IO without changing semantics.
const peekBytes = 4096

// Comment-prefix styles. Pass to Header.
const (
	// StyleHash is "#" — used by YAML, nginx, Apache, MySQL .cnf,
	// .ini, fish/bash scripts, Dockerfile.
	StyleHash = "#"

	// StyleSlash is "//" — used by PHP, JS, Go, C-family.
	StyleSlash = "//"
)

// ErrUserOwned reports that a file exists without the Locorum marker
// so WriteIfManaged refused to overwrite it. Callers compare with
// errors.Is. The contract is "we did not write"; callers that need
// stronger guarantees should re-read the file.
var ErrUserOwned = errors.New("genmark: file is user-owned (no locorum-generated marker)")

// Header returns the canonical two-line generated-file header in the
// requested comment style, plus a trailing blank line so subsequent
// content is visually separated.
//
// style is typically StyleHash or StyleSlash; any non-empty token is
// permitted (";" for INI dialects, "--" for SQL, "REM" for batch). An
// empty style falls back to StyleHash for safety — the marker still
// matches.
func Header(style string) string {
	if style == "" {
		style = StyleHash
	}
	return fmt.Sprintf(
		"%s locorum-generated — DO NOT EDIT.\n%s Remove this line to take ownership; Locorum will then leave this file alone.\n\n",
		style, style,
	)
}

// HasMarker reports whether body's leading peekBytes contain Marker.
// Empty bodies return false — an empty file is treated as user-owned
// (we have no way to tell a fresh-truncate from a wiped-by-user file,
// so we err on the side of preserving user state).
func HasMarker(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if len(body) > peekBytes {
		body = body[:peekBytes]
	}
	return bytes.Contains(body, []byte(Marker))
}

// HasMarkerFile is HasMarker but reads from disk. Returns (false, nil)
// when the file does not exist — the absence of a file is not an
// error, callers decide what to do. Permission errors and IO faults
// are returned so callers can distinguish "missing" from "unreadable".
func HasMarkerFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	buf := make([]byte, peekBytes)
	n, err := io.ReadFull(f, buf)
	switch {
	case err == nil, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		// All three are non-fatal: full read, empty file, partial read.
	default:
		return false, err
	}
	return HasMarker(buf[:n]), nil
}

// WriteIfManaged writes data atomically to path if the file is missing
// or already carries Marker. Returns ErrUserOwned without writing when
// the file exists without the marker.
//
// data must already include a Header — this function does not prepend
// one. A byte-equal existing file is a fast no-op (no temp file, no
// rename, no fsync) so callers can invoke it on every site-start
// without worrying about IO churn.
//
// perm is the destination file mode; the temp file is chmod'd to perm
// before rename so the visible file never widens past requested.
func WriteIfManaged(path string, data []byte, perm os.FileMode) error {
	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return WriteAtomic(path, data, perm)
	case err != nil:
		return fmt.Errorf("genmark: read existing %q: %w", path, err)
	}
	if bytes.Equal(existing, data) {
		return nil
	}
	if !HasMarker(existing) {
		slog.Warn("genmark: refusing to overwrite user-managed file",
			"path", path,
			"hint", "remove the file or restore a `locorum-generated` line to opt back in")
		return ErrUserOwned
	}
	return writeAtomicNoCheck(path, data, perm)
}

// WriteAtomic writes data to path via temp + rename, regardless of
// whether the existing file carries the marker. Use for files Locorum
// exclusively owns (router dynamic configs, generated includes the
// user is not expected to hand-edit).
//
// Idempotency: a byte-equal existing file is left in place untouched.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return writeAtomicNoCheck(path, data, perm)
}

// writeAtomicNoCheck is the inner write — assumes the caller already
// decided to overwrite.  Splitting the byte-equal short-circuit out
// keeps WriteIfManaged from re-reading the file in the rare path where
// the existing bytes equal the new bytes (we already have `existing`
// in memory at that point).
//
// Atomicity: rename(2) on the same filesystem is atomic on POSIX, and
// MoveFileEx with MOVEFILE_REPLACE_EXISTING is atomic on NTFS/ReFS
// since Windows Vista. The temp file lives in the destination's
// directory so the rename never crosses filesystems.
//
// Crash safety: data is fsync'd before close; the rename is the
// commit point. A crash before rename leaves only the .tmp file (and
// callers always clean up on error).
func writeAtomicNoCheck(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("genmark: ensure parent dir %q: %w", dir, err)
	}

	// CreateTemp's pattern keeps the temp file hidden (leading dot)
	// and unique (the random suffix). We deliberately do not use
	// renameio or moby/atomicwriter — the dependency cost is not
	// worth it for ~30 lines of stdlib.
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("genmark: create temp in %q: %w", dir, err)
	}
	tmpName := f.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("genmark: write temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return fmt.Errorf("genmark: sync temp: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return fmt.Errorf("genmark: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("genmark: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("genmark: rename: %w", err)
	}
	return nil
}
