package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TempLocorumHome points HOME (USERPROFILE on Windows) at a fresh
// t.TempDir() and pre-creates ~/.locorum/. Locorum reads its home via
// os.UserHomeDir, so storage/config/snapshots/hooks/asset-reconciler
// all pick up the isolated tree. Env is restored via t.Cleanup.
//
// Not safe for parallel sibling tests — they share process env.
func TempLocorumHome(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()

	keys := []string{"HOME"}
	if runtime.GOOS == "windows" {
		keys = []string{"USERPROFILE", "HOME"}
	}

	for _, k := range keys {
		old, hadOld := os.LookupEnv(k)
		if err := os.Setenv(k, dir); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
		t.Cleanup(func() {
			if hadOld {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		})
	}

	if err := os.MkdirAll(filepath.Join(dir, ".locorum"), 0o755); err != nil {
		t.Fatalf("mkdir locorum home: %v", err)
	}
	return dir
}
