package tls

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMkcertDownloadURL(t *testing.T) {
	url, err := mkcertDownloadURL()
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "freebsd" {
		if err != nil {
			t.Fatalf("expected URL on %s/%s, got err: %v", runtime.GOOS, runtime.GOARCH, err)
		}
		want := "?for=" + runtime.GOOS + "/" + runtime.GOARCH
		if !strings.Contains(url, want) {
			t.Errorf("URL missing platform query: %q", url)
		}
		if !strings.Contains(url, MkcertVersion) {
			t.Errorf("URL missing pinned version: %q", url)
		}
	}
}

func TestResolveBinaryPrefersBinDir(t *testing.T) {
	// A fake mkcert in a private binDir is preferred over $PATH. The
	// fake doesn't need to be functional — resolveBinary only checks
	// stat + executable bit, never invokes it.
	binDir := t.TempDir()
	name := "mkcert"
	if runtime.GOOS == "windows" {
		name = "mkcert.exe"
	}
	fake := filepath.Join(binDir, name)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := NewMkcert(t.TempDir(), binDir)
	got := m.resolveBinary()
	if got != fake {
		t.Errorf("resolveBinary = %q, want %q", got, fake)
	}
}

func TestResolveBinaryEmptyBinDir(t *testing.T) {
	// With no binDir, resolution falls back to PATH (which may or may
	// not have mkcert). We only assert the function doesn't panic and
	// returns either "" or a real path on PATH.
	m := NewMkcert(t.TempDir(), "")
	got := m.resolveBinary()
	if got != "" {
		if _, err := os.Stat(got); err != nil {
			t.Errorf("resolveBinary returned non-existent path %q: %v", got, err)
		}
	}
}

func TestEnsureBinaryNoBinDirSkipsDownload(t *testing.T) {
	// Empty binDir + no resolvable binary on this test's exe-relative
	// candidates should error rather than reach the network.
	m := NewMkcert(t.TempDir(), "")

	// Skip if mkcert happens to be on $PATH — resolveBinary would
	// short-circuit and EnsureBinary would succeed.
	if m.resolveBinary() != "" {
		t.Skip("mkcert resolvable on this host; can't verify no-download path")
	}

	if _, err := m.EnsureBinary(t.Context()); err == nil {
		t.Error("expected error when binDir is empty and no binary found")
	}
}
