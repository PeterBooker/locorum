//go:build windows

package platform

import (
	"errors"
	"log/slog"

	"golang.org/x/sys/windows/registry"
)

// longPathsRegistryKey is the canonical location of the LongPathsEnabled
// flag — set in Win10 1607+ to opt the OS into >MAX_PATH file paths for
// processes that also opt in via their manifest. Older Windows builds
// don't carry the value at all; we treat that case as "off".
const (
	longPathsRegistryKey   = `SYSTEM\CurrentControlSet\Control\FileSystem`
	longPathsRegistryValue = `LongPathsEnabled`
)

// readLongPathsEnabled returns whether the OS has the LongPathsEnabled
// flag set to 1. Treats every failure mode (missing key, missing value,
// access denied, non-DWORD type) as "off" — the safer direction for a
// gating check that errs on the side of refusing to create a site whose
// path might breach MAX_PATH.
//
// All non-NotFound failures are logged at debug level so an operator
// can diagnose why a Windows host with the flag set is being treated as
// off (e.g. the GUI is running under a service account without registry
// read permission).
func readLongPathsEnabled() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, longPathsRegistryKey, registry.QUERY_VALUE)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			slog.Debug("platform: open LongPathsEnabled key", "err", err.Error())
		}
		return false
	}
	defer k.Close()

	v, _, err := k.GetIntegerValue(longPathsRegistryValue)
	if err != nil {
		if !errors.Is(err, registry.ErrNotExist) {
			slog.Debug("platform: read LongPathsEnabled value", "err", err.Error())
		}
		return false
	}
	return v == 1
}
