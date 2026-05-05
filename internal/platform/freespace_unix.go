//go:build !windows

package platform

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// hostFreeBytes reads the unprivileged-user-available bytes from statfs(2).
//
// We use Bavail (blocks available to non-root) rather than Bfree (blocks
// available to root) so the number lines up with what the user actually
// experiences when they try to download a 5GB Docker image into the
// reserved-for-root region.
func hostFreeBytes(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("hostFreeBytes: empty path")
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	// Bavail × Bsize. Both are unsigned but the result fits in int64 for
	// any plausible filesystem size on consumer hardware (max int64 is
	// 9.2 EB).
	avail := uint64(st.Bavail) * uint64(st.Bsize)
	if avail > 1<<62 {
		// Defensive overflow guard; one petabyte+ on a workstation is
		// implausible but report a sentinel rather than wrap to negative.
		return 1 << 62, nil
	}
	return int64(avail), nil
}
