package sites

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSnapshotName(t *testing.T) {
	ts := time.Date(2026, 4, 27, 21, 0, 0, 0, time.UTC)
	cases := []struct {
		slug, label, engine, version, want string
	}{
		{
			slug: "myblog", label: "pre_delete", engine: "mysql", version: "8.0",
			want: "myblog--pre_delete--20260427T210000Z--mysql-8.0.sql.gz",
		},
		{
			slug: "my-store", label: "manual", engine: "MariaDB", version: "10.11",
			want: "my-store--manual--20260427T210000Z--mariadb-10.11.sql.gz",
		},
		{
			// Hostile inputs get sanitised; nothing escapes the
			// filename via path separators or shell metacharacters.
			slug: "store", label: "manual", engine: " evil/../engine ", version: "$(rm -rf)",
			want: "store--manual--20260427T210000Z--evilengine-rm-rf.sql.gz",
		},
	}
	for _, c := range cases {
		got := buildSnapshotName(c.slug, c.label, c.engine, c.version, ts)
		if got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestParseSnapshotName(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		ts := time.Date(2026, 4, 27, 21, 0, 0, 0, time.UTC)
		name := buildSnapshotName("myblog", "pre_delete", "mysql", "8.0", ts)
		info := parseSnapshotName(name)
		if info == nil {
			t.Fatalf("parse returned nil for %q", name)
		}
		if info.Slug != "myblog" || info.Label != "pre_delete" || info.Engine != "mysql" || info.Version != "8.0" {
			t.Errorf("parsed = %+v, want slug=myblog label=pre_delete engine=mysql version=8.0", info)
		}
		if !info.CreatedAt.Equal(ts) {
			t.Errorf("CreatedAt = %v, want %v", info.CreatedAt, ts)
		}
	})

	t.Run("hyphenated slug round-trips", func(t *testing.T) {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		name := buildSnapshotName("my-store-prod", "manual", "mariadb", "10.11.5", ts)
		info := parseSnapshotName(name)
		if info == nil {
			t.Fatalf("parse returned nil for %q", name)
		}
		if info.Slug != "my-store-prod" || info.Engine != "mariadb" || info.Version != "10.11.5" {
			t.Errorf("parsed = %+v", info)
		}
	})

	t.Run("ignores unrecognised", func(t *testing.T) {
		bad := []string{
			"random.txt",
			"myblog.sql",
			"myblog_label.sql.gz",
			"myblog--label.sql.gz", // missing ts/engine
			"myblog--label--2026-04-27--mysql-8.0.sql.gz", // wrong ts format
			"--label--20260427T210000Z--mysql-8.0.sql.gz", // empty slug
		}
		for _, n := range bad {
			if info := parseSnapshotName(n); info != nil {
				t.Errorf("parsed unrecognised name %q as %+v", n, info)
			}
		}
	})
}

func TestSnapshotLabelValidation(t *testing.T) {
	good := []string{"pre_delete", "manual", "abc", "x1", "a_b_c"}
	for _, l := range good {
		if !snapshotLabelPat.MatchString(l) {
			t.Errorf("good label %q rejected", l)
		}
	}
	bad := []string{
		"",
		"_starts_underscore",
		"has-dash", // dashes break filename parsing — disallowed
		"has space",
		"has/slash",
		"upper_Case",
		strings.Repeat("a", 33),
	}
	for _, l := range bad {
		if snapshotLabelPat.MatchString(l) {
			t.Errorf("bad label %q accepted", l)
		}
	}
}

func TestGzipMoveAtomicAndGunzip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "raw.sql")
	body := []byte("-- mysqldump\nINSERT INTO wp_posts VALUES (1);\n")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out.sql.gz")
	if err := gzipMoveAtomic(src, dst); err != nil {
		t.Fatalf("gzipMoveAtomic: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src should be removed; stat err=%v", err)
	}

	// Verify gzip-readable + content matches.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("round-trip mismatch:\n got = %q\nwant = %q", got, body)
	}

	// And the inverse: gunzipTo extracts identical bytes.
	restored := filepath.Join(dir, "restored.sql")
	if err := gunzipTo(dst, restored); err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if string(got2) != string(body) {
		t.Errorf("gunzipTo round-trip mismatch")
	}
}

func TestGzipMoveAtomic_NoLeftoverTmpOnFailure(t *testing.T) {
	dir := t.TempDir()
	// Source doesn't exist → open fails → no tmpfile should be created.
	err := gzipMoveAtomic(filepath.Join(dir, "missing.sql"), filepath.Join(dir, "out.sql.gz"))
	if err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".locorum-snap-") {
			t.Errorf("tmpfile left behind: %s", e.Name())
		}
	}
}
