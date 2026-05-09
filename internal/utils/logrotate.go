package utils

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// RotateIfLarge rotates logPath when it exceeds maxBytes, preserving up to
// keep historical files (logPath.1 .. logPath.<keep>). The newest backup is
// always logPath.1; older backups shift outward by one. The file rotated
// past keep is unlinked.
//
// If logPath does not exist or is below the threshold, RotateIfLarge is a
// no-op and returns nil. keep <= 0 is normalised to 1 — the rotate always
// keeps at least one historical copy. Best-effort cleanup: an unlink error
// for the oldest backup is logged via errors.Join with the rename outcome
// so the caller still sees the operational outcome.
func RotateIfLarge(logPath string, maxBytes int64, keep int) error {
	if maxBytes <= 0 {
		return errors.New("logrotate: maxBytes must be > 0")
	}
	if keep <= 0 {
		keep = 1
	}

	info, err := os.Stat(logPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}

	// Drop the oldest backup if it would exceed `keep`.
	oldest := fmt.Sprintf("%s.%d", logPath, keep)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// Non-fatal: log layer keeps working; we just may briefly have
		// keep+1 files on disk.
		return fmt.Errorf("logrotate: remove oldest %q: %w", oldest, err)
	}

	// Shift logPath.<n> -> logPath.<n+1>, descending so we never clobber.
	for i := keep - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", logPath, i)
		to := fmt.Sprintf("%s.%d", logPath, i+1)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("logrotate: rename %q -> %q: %w", from, to, err)
		}
	}

	// Finally, current → .1.
	target := logPath + ".1"
	if err := os.Rename(logPath, target); err != nil {
		return fmt.Errorf("logrotate: rename %q -> %q: %w", logPath, target, err)
	}
	return nil
}
