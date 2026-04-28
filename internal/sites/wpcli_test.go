package sites

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func TestNormaliseInContainerDocroot(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"/":         "",
		".":         "",
		"wordpress": "wordpress",
		"/web":      "web",
		"//deep":    "deep",
		"sub/dir":   "sub/dir",
	}
	for in, want := range cases {
		if got := normaliseInContainerDocroot(in); got != want {
			t.Errorf("normaliseInContainerDocroot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInContainerWPPath(t *testing.T) {
	cases := []struct {
		publicDir, want string
	}{
		{"", "/var/www/html"},
		{"/", "/var/www/html"},
		{"wordpress", "/var/www/html/wordpress"},
		{"/web", "/var/www/html/web"},
	}
	for _, c := range cases {
		got := inContainerWPPath(&types.Site{PublicDir: c.publicDir})
		if got != c.want {
			t.Errorf("inContainerWPPath(%q) = %q, want %q", c.publicDir, got, c.want)
		}
	}
}

func TestDetectWordPress(t *testing.T) {
	t.Run("empty dir → no matches, no error", func(t *testing.T) {
		dir := t.TempDir()
		matches, err := detectWordPress(dir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matches) != 0 {
			t.Errorf("got %v, want []", matches)
		}
	})

	t.Run("missing dir → no matches, no error", func(t *testing.T) {
		matches, err := detectWordPress(t.TempDir()+"/does-not-exist", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matches) != 0 {
			t.Errorf("got %v, want []", matches)
		}
	})

	t.Run("wp at docroot", func(t *testing.T) {
		dir := t.TempDir()
		mustTouch(t, filepath.Join(dir, "wp-settings.php"))
		matches, err := detectWordPress(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0] != "." {
			t.Errorf("got %v, want [.]", matches)
		}
	})

	t.Run("wp one level deep", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "wordpress")
		_ = os.MkdirAll(nested, 0o755)
		mustTouch(t, filepath.Join(nested, "wp-settings.php"))
		matches, err := detectWordPress(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0] != "wordpress" {
			t.Errorf("got %v, want [wordpress]", matches)
		}
	})

	t.Run("ambiguous → error", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, "a"), 0o755)
		_ = os.MkdirAll(filepath.Join(dir, "b"), 0o755)
		mustTouch(t, filepath.Join(dir, "a", "wp-settings.php"))
		mustTouch(t, filepath.Join(dir, "b", "wp-settings.php"))
		_, err := detectWordPress(dir, "")
		if err == nil {
			t.Fatal("expected ambiguity error, got nil")
		}
	})

	t.Run("ambiguous root + nested → error", func(t *testing.T) {
		dir := t.TempDir()
		mustTouch(t, filepath.Join(dir, "wp-settings.php"))
		_ = os.MkdirAll(filepath.Join(dir, "wordpress"), 0o755)
		mustTouch(t, filepath.Join(dir, "wordpress", "wp-settings.php"))
		_, err := detectWordPress(dir, "")
		if err == nil {
			t.Fatal("expected ambiguity error")
		}
	})

	t.Run("wp-content at root is not a match", func(t *testing.T) {
		// wp-content/wp-settings.php would be a malformed install, but
		// even if it existed we explicitly skip wp-content / wp-admin /
		// wp-includes during depth-1 scan because they are subdirs of an
		// install, not a parallel install.
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, "wp-content"), 0o755)
		mustTouch(t, filepath.Join(dir, "wp-content", "wp-settings.php"))
		matches, err := detectWordPress(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("got %v, want []", matches)
		}
	})

	t.Run("respects publicDir", func(t *testing.T) {
		dir := t.TempDir()
		nested := filepath.Join(dir, "web")
		_ = os.MkdirAll(nested, 0o755)
		mustTouch(t, filepath.Join(nested, "wp-settings.php"))
		matches, err := detectWordPress(dir, "web")
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0] != "." {
			t.Errorf("got %v, want [.] (relative to publicDir)", matches)
		}
	})
}

func mustTouch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}
