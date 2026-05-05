//go:build windows

package platform

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// hostFreeBytes calls GetDiskFreeSpaceExW. The "available" return value is
// the bytes available to the calling user — accounts for per-user disk
// quotas if any are in effect.
func hostFreeBytes(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("hostFreeBytes: empty path")
	}
	utf16Path, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("convert path: %w", err)
	}
	var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes uint64
	if err := windows.GetDiskFreeSpaceEx(utf16Path, &freeBytesAvailable, &totalNumberOfBytes, &totalNumberOfFreeBytes); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx %q: %w", path, err)
	}
	if freeBytesAvailable > 1<<62 {
		return 1 << 62, nil
	}
	return int64(freeBytesAvailable), nil
}
