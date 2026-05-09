package platform

import (
	"strings"
	"testing"
)

// DockerPath must produce a slash-only, idempotent, length-preserving
// translation of the input.
func FuzzDockerPath(f *testing.F) {
	seeds := []string{
		"",
		"/home/user/site",
		`C:\Users\user\site`,
		`C:/mixed\slashes`,
		"/mnt/c/Users/foo",
		"\x00\\:relative\\path",
		strings.Repeat("a/", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		out := DockerPath(p)
		if strings.Contains(out, `\`) {
			t.Fatalf("backslash survived in %q -> %q", p, out)
		}
		if DockerPath(out) != out {
			t.Fatalf("not idempotent: %q -> %q -> %q", p, out, DockerPath(out))
		}
		if len(out) != len(p) {
			t.Fatalf("length changed: %q (len=%d) -> %q (len=%d)", p, len(p), out, len(out))
		}
	})
}

func FuzzIsMntDrvFsPath(f *testing.F) {
	seeds := []string{
		"/mnt/c", "/mnt/c/Users", "/mnt/Z/foo", "/mnt/cc/foo",
		"/MNT/C/foo", "/home/user/locorum",
		"", "\x00", "/mnt/", "//mnt/c",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		got := isMntDrvFsPath(p)
		if isMntDrvFsPath(p) != got {
			t.Fatal("non-deterministic")
		}
	})
}
