package genmark

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeader(t *testing.T) {
	cases := []struct {
		name      string
		style     string
		mustStart string
	}{
		{"hash", StyleHash, "# locorum-generated"},
		{"slash", StyleSlash, "// locorum-generated"},
		{"empty defaults to hash", "", "# locorum-generated"},
		{"semicolon", ";", "; locorum-generated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := Header(tc.style)
			if !strings.HasPrefix(h, tc.mustStart) {
				t.Fatalf("expected header to start with %q, got %q", tc.mustStart, h)
			}
			if !strings.HasSuffix(h, "\n\n") {
				t.Fatalf("expected trailing blank line, got %q", h)
			}
			if !HasMarker([]byte(h)) {
				t.Fatalf("Header() output must contain Marker; got %q", h)
			}
		})
	}
}

func TestHasMarker(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		want bool
	}{
		{"empty", []byte{}, false},
		{"nil", nil, false},
		{"plain text without marker", []byte("hello world\nno marker here"), false},
		{"marker on first line", []byte("# locorum-generated — DO NOT EDIT.\n\nbody"), true},
		{"marker on second line (PHP shebang)", []byte("<?php\n// locorum-generated — DO NOT EDIT.\n"), true},
		{"marker as bare substring", []byte("xx locorum-generated xx"), true},
		{"marker just inside peek window", append(bytes.Repeat([]byte(" "), peekBytes-len(Marker)-1), []byte(Marker+"x")...), true},
		{"marker just past peek window", append(bytes.Repeat([]byte(" "), peekBytes), []byte(Marker)...), false},
		{"case-mismatched marker is ignored", []byte("LOCORUM-GENERATED"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasMarker(tc.body); got != tc.want {
				t.Fatalf("HasMarker(%q) = %v; want %v", trunc(tc.body), got, tc.want)
			}
		})
	}
}

func TestHasMarkerFile(t *testing.T) {
	dir := t.TempDir()

	// Missing file: no error, no marker.
	got, err := HasMarkerFile(filepath.Join(dir, "missing.txt"))
	if err != nil {
		t.Fatalf("missing file: unexpected err: %v", err)
	}
	if got {
		t.Fatalf("missing file: expected false, got true")
	}

	// File with marker.
	managed := filepath.Join(dir, "managed.yaml")
	if err := os.WriteFile(managed, []byte(Header(StyleHash)+"key: value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasMarkerFile(managed)
	if err != nil || !got {
		t.Fatalf("managed: got=%v err=%v; want true,nil", got, err)
	}

	// File without marker.
	user := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(user, []byte("key: value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasMarkerFile(user)
	if err != nil || got {
		t.Fatalf("user: got=%v err=%v; want false,nil", got, err)
	}

	// Empty file: no marker.
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = HasMarkerFile(empty)
	if err != nil || got {
		t.Fatalf("empty: got=%v err=%v; want false,nil", got, err)
	}
}

func TestWriteIfManaged_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.yaml")
	body := []byte(Header(StyleHash) + "key: value\n")

	if err := WriteIfManaged(path, body, 0o644); err != nil {
		t.Fatalf("WriteIfManaged: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("content mismatch:\ngot:\n%s\nwant:\n%s", got, body)
	}
	assertMode(t, path, 0o644)
}

func TestWriteIfManaged_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "idempotent.yaml")
	body := []byte(Header(StyleHash) + "key: value\n")

	if err := WriteIfManaged(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	stat1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Second write with identical bytes must be a no-op (mtime
	// preserved). This is what allows callers to invoke us on every
	// regenerate without waking inotify watchers.
	if err := WriteIfManaged(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	stat2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stat1.ModTime().Equal(stat2.ModTime()) {
		t.Fatalf("idempotent write changed mtime: %v -> %v", stat1.ModTime(), stat2.ModTime())
	}

	// Also: no leftover temp files.
	assertNoTempFiles(t, dir)
}

func TestWriteIfManaged_OverwriteWhenManaged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.yaml")
	old := []byte(Header(StyleHash) + "key: old\n")
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}

	updated := []byte(Header(StyleHash) + "key: new\n")
	if err := WriteIfManaged(path, updated, 0o644); err != nil {
		t.Fatalf("WriteIfManaged: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, updated) {
		t.Fatalf("content not updated:\ngot:\n%s\nwant:\n%s", got, updated)
	}
}

func TestWriteIfManaged_RefusesUserOwned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	user := []byte("key: user-owned\n# I deleted the marker on purpose\n")
	if err := os.WriteFile(path, user, 0o600); err != nil {
		t.Fatal(err)
	}

	updated := []byte(Header(StyleHash) + "key: locorum\n")
	err := WriteIfManaged(path, updated, 0o644)
	if !errors.Is(err, ErrUserOwned) {
		t.Fatalf("expected ErrUserOwned, got %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, user) {
		t.Fatalf("user-owned content was modified:\ngot:\n%s\nwant:\n%s", got, user)
	}
	// Permissions also preserved (0o600 from the original).
	assertMode(t, path, 0o600)
	assertNoTempFiles(t, dir)
}

func TestWriteAtomic_OverwritesUnconditionally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "always.yaml")

	// User-owned file (no marker) — WriteAtomic ignores ownership.
	if err := os.WriteFile(path, []byte("user content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	updated := []byte(Header(StyleHash) + "managed: true\n")
	if err := WriteAtomic(path, updated, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, updated) {
		t.Fatalf("WriteAtomic did not overwrite:\ngot:\n%s\nwant:\n%s", got, updated)
	}
}

func TestWriteAtomic_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "config.yaml")
	body := []byte(Header(StyleHash) + "x: 1\n")

	if err := WriteAtomic(path, body, 0o644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("content mismatch:\ngot:\n%s\nwant:\n%s", got, body)
	}
}

func TestWriteAtomic_NoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.yaml")
	body := []byte(Header(StyleHash) + "ok: true\n")

	if err := WriteAtomic(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	assertNoTempFiles(t, dir)
}

func TestWriteAtomic_RestrictedPerms(t *testing.T) {
	if os.Geteuid() == 0 {
		// Running as root: chmod won't simulate a "could not chmod"
		// failure cleanly. Skip; coverage from non-root is enough.
		t.Skip("skipped under root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "restricted.yaml")
	body := []byte(Header(StyleHash) + "secret: yes\n")

	if err := WriteAtomic(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Fatalf("mode of %q: got %o; want %o", path, got, want)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// Recursively check subdirs too — WriteAtomic may have made
		// parents.
		if e.IsDir() {
			assertNoTempFiles(t, filepath.Join(dir, e.Name()))
			continue
		}
		name := e.Name()
		if strings.Contains(name, ".tmp-") {
			t.Fatalf("leftover temp file: %s", filepath.Join(dir, name))
		}
	}
}

// trunc is a small helper for keeping test failure messages readable.
func trunc(b []byte) string {
	const limit = 60
	s := string(b)
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
