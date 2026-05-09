package sites

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestBuildSnapshotName(t *testing.T) {
	ts := time.Date(2026, 4, 27, 21, 0, 0, 0, time.UTC)
	cases := []struct {
		slug, label, engine, version, codec, want string
	}{
		{
			slug: "myblog", label: "pre_delete", engine: "mysql", version: "8.0", codec: codecZstd,
			want: "myblog--pre_delete--20260427T210000Z--mysql-8.0.sql.zst",
		},
		{
			slug: "my-store", label: "manual", engine: "MariaDB", version: "10.11", codec: codecGzip,
			want: "my-store--manual--20260427T210000Z--mariadb-10.11.sql.gz",
		},
		{
			// Hostile inputs get sanitised; nothing escapes the
			// filename via path separators or shell metacharacters.
			slug: "store", label: "manual", engine: " evil/../engine ", version: "$(rm -rf)", codec: codecZstd,
			want: "store--manual--20260427T210000Z--evilengine-rm-rf.sql.zst",
		},
	}
	for _, c := range cases {
		got := buildSnapshotName(c.slug, c.label, c.engine, c.version, ts, c.codec)
		if got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestParseSnapshotName(t *testing.T) {
	t.Run("zstd round-trip", func(t *testing.T) {
		ts := time.Date(2026, 4, 27, 21, 0, 0, 0, time.UTC)
		name := buildSnapshotName("myblog", "pre_delete", "mysql", "8.0", ts, codecZstd)
		info := parseSnapshotName(name)
		if info == nil {
			t.Fatalf("parse returned nil for %q", name)
		}
		if info.Slug != "myblog" || info.Label != "pre_delete" || info.Engine != "mysql" || info.Version != "8.0" {
			t.Errorf("parsed = %+v", info)
		}
		if info.Compression != codecZstd {
			t.Errorf("Compression = %q, want %q", info.Compression, codecZstd)
		}
		if !info.CreatedAt.Equal(ts) {
			t.Errorf("CreatedAt = %v, want %v", info.CreatedAt, ts)
		}
	})

	t.Run("gzip legacy round-trip", func(t *testing.T) {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		name := buildSnapshotName("my-store-prod", "manual", "mariadb", "10.11.5", ts, codecGzip)
		info := parseSnapshotName(name)
		if info == nil {
			t.Fatalf("parse returned nil for %q", name)
		}
		if info.Slug != "my-store-prod" || info.Engine != "mariadb" || info.Version != "10.11.5" {
			t.Errorf("parsed = %+v", info)
		}
		if info.Compression != codecGzip {
			t.Errorf("Compression = %q, want %q", info.Compression, codecGzip)
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

func TestCompressWriter_RoundTrip_Zstd(t *testing.T) {
	body := []byte("-- mysqldump\nINSERT INTO wp_posts VALUES (1);\n")

	var buf bytes.Buffer
	cw, err := newCompressWriter(codecZstd, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	dr, err := zstd.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer dr.Close()
	got, err := io.ReadAll(dr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("round-trip mismatch:\n got = %q\nwant = %q", got, body)
	}
}

func TestCompressWriter_RoundTrip_Gzip(t *testing.T) {
	body := []byte("-- mysqldump\nINSERT INTO wp_posts VALUES (1);\n")

	var buf bytes.Buffer
	cw, err := newCompressWriter(codecGzip, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}

	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("round-trip mismatch:\n got = %q\nwant = %q", got, body)
	}
}

func TestNewDecompressReader_DispatchesByExtension(t *testing.T) {
	dir := t.TempDir()
	body := []byte("CREATE TABLE x (id INT);\n")

	t.Run("zstd", func(t *testing.T) {
		path := filepath.Join(dir, "out.sql.zst")
		f, _ := os.Create(path)
		w, _ := zstd.NewWriter(f)
		_, _ = w.Write(body)
		_ = w.Close()
		_ = f.Close()

		f2, _ := os.Open(path)
		defer f2.Close()
		r, err := newDecompressReader(path, f2)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		if !bytes.Equal(got, body) {
			t.Errorf("got %q, want %q", got, body)
		}
	})

	t.Run("gzip", func(t *testing.T) {
		path := filepath.Join(dir, "out.sql.gz")
		f, _ := os.Create(path)
		w := gzip.NewWriter(f)
		_, _ = w.Write(body)
		_ = w.Close()
		_ = f.Close()

		f2, _ := os.Open(path)
		defer f2.Close()
		r, err := newDecompressReader(path, f2)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		if !bytes.Equal(got, body) {
			t.Errorf("got %q, want %q", got, body)
		}
	})

	t.Run("unknown extension rejected", func(t *testing.T) {
		f, _ := os.Open(os.DevNull)
		defer f.Close()
		if _, err := newDecompressReader("/tmp/x.tar", f); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestWriteAndVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.sql.zst")
	body := []byte("body bytes")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(body)
	digest := hex.EncodeToString(h[:])
	if err := writeChecksum(path, digest); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Tamper.
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyChecksum_NoSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.sql.zst")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyChecksum(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no checksum") && err.Error() != errNoChecksumSidecar.Error() {
		t.Errorf("expected errNoChecksumSidecar, got %v", err)
	}
}
