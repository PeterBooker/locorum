package sites

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterImportStream_DropsCreateDatabaseAndUse(t *testing.T) {
	in := strings.Join([]string{
		`-- mysqldump preamble`,
		`CREATE DATABASE IF NOT EXISTS prod;`,
		`USE prod;`,
		`  use Prod ;`,
		`  CREATE   DATABASE   foo;`,
		`CREATE TABLE wp_options (...);`,
		`INSERT INTO wp_options VALUES (1, 'siteurl', 'https://prod.example.com');`,
	}, "\n") + "\n"

	var out bytes.Buffer
	n, err := FilterImportStream(strings.NewReader(in), &out)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if n == 0 {
		t.Fatal("filter wrote zero bytes")
	}

	got := out.String()
	for _, dropped := range []string{"CREATE DATABASE", "USE prod", "use Prod"} {
		if strings.Contains(got, dropped) {
			t.Errorf("expected %q to be dropped, output:\n%s", dropped, got)
		}
	}
	for _, kept := range []string{"-- mysqldump preamble", "CREATE TABLE wp_options", "INSERT INTO wp_options"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %q to be kept, output:\n%s", kept, got)
		}
	}
}

func TestFilterImportStream_StripsSandboxComment(t *testing.T) {
	in := "/*!999999\\- enable the sandbox mode */\n" +
		"CREATE TABLE wp_users (id INT);\n"
	var out bytes.Buffer
	if _, err := FilterImportStream(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sandbox mode") {
		t.Errorf("sandbox comment not stripped:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CREATE TABLE wp_users") {
		t.Errorf("table line stripped accidentally:\n%s", out.String())
	}
}

func TestFilterImportStream_RewritesUCA1400Collation(t *testing.T) {
	in := "CREATE TABLE x (n VARCHAR(10) COLLATE utf8mb4_uca1400_ai_ci);\n" +
		"-- ALTER TABLE y MODIFY z TEXT COLLATE utf8mb4_uca1400_estonian_as_cs;\n"
	var out bytes.Buffer
	if _, err := FilterImportStream(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "uca1400") {
		t.Errorf("uca1400 not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "utf8mb4_unicode_ci") {
		t.Errorf("expected utf8mb4_unicode_ci replacement:\n%s", got)
	}
}

func TestFilterImportStream_StripsDefiner(t *testing.T) {
	in := "CREATE DEFINER=`prod`@`%` VIEW v_users AS SELECT id FROM wp_users;\n" +
		"CREATE DEFINER=`x`@`localhost` PROCEDURE p() BEGIN SELECT 1; END;\n"
	var out bytes.Buffer
	if _, err := FilterImportStream(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "DEFINER=") {
		t.Errorf("DEFINER not stripped:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CREATE  VIEW v_users") &&
		!strings.Contains(out.String(), "CREATE VIEW v_users") {
		t.Errorf("view declaration mangled:\n%s", out.String())
	}
}

func TestFilterImportStream_PreservesUnicodeAndNewlines(t *testing.T) {
	in := "INSERT INTO wp_posts VALUES (1, 'Héllo 世界');\n" +
		"-- comment with é\n"
	var out bytes.Buffer
	if _, err := FilterImportStream(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Héllo 世界") {
		t.Errorf("unicode mangled:\n%s", out.String())
	}
}

func TestFilterImportStream_FailsOnOversizedLine(t *testing.T) {
	huge := bytes.Repeat([]byte{'A'}, importMaxLineBytes+10)
	var out bytes.Buffer
	_, err := FilterImportStream(bytes.NewReader(huge), &out)
	if err == nil {
		t.Fatal("expected oversized-line error, got nil")
	}
	if !strings.Contains(err.Error(), "longer than") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestLooksLikeSQL(t *testing.T) {
	yes := []string{
		"-- MySQL dump 10.13",
		"/*!40101 SET CHARACTER_SET_CLIENT */",
		"SET FOREIGN_KEY_CHECKS=0;",
		"DROP TABLE IF EXISTS wp_users;",
		"CREATE TABLE wp_users (id INT);",
		"INSERT INTO wp_users VALUES (1);",
		"\xef\xbb\xbf-- BOM-prefixed dump",
		"\n\n  -- after blank lines",
	}
	for _, s := range yes {
		if !looksLikeSQL([]byte(s)) {
			t.Errorf("expected SQL: %q", s)
		}
	}
	no := []string{
		"",
		"PK\x03\x04...", // zip header
		"\x1f\x8b...",   // gzip header
		"hello world",
		"<?xml version='1.0'?>",
	}
	for _, s := range no {
		if looksLikeSQL([]byte(s)) {
			t.Errorf("expected not-SQL: %q", s)
		}
	}
}

func TestFlipScheme(t *testing.T) {
	cases := map[string]string{
		"https://example.com":     "http://example.com",
		"http://example.com/path": "https://example.com/path",
		"https://x.test:8080/a?b": "http://x.test:8080/a?b",
		"ftp://example.com":       "",
		"not a url":               "",
	}
	for in, want := range cases {
		if got := flipScheme(in); got != want {
			t.Errorf("flipScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDedupePairs(t *testing.T) {
	in := []SearchReplacePair{
		{"a", "b"},
		{"a", "b"},
		{"c", "d"},
		{"a", "b"},
		{"e", "f"},
	}
	got := dedupePairs(in)
	want := []SearchReplacePair{{"a", "b"}, {"c", "d"}, {"e", "f"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %v, want %v", i, got[i], want[i])
		}
	}
}

// prepareDump tests cover the full pipeline (decompress + filter + write).

func TestPrepareDump_PlainSQL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.sql")
	mustWrite(t, src, []byte(
		"-- MySQL dump\n"+
			"CREATE DATABASE prod;\n"+
			"USE prod;\n"+
			"CREATE TABLE wp_users (id INT);\n"+
			"INSERT INTO wp_users VALUES (1);\n",
	))
	dst := filepath.Join(dir, "out.sql")
	if err := prepareDump(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "CREATE DATABASE") {
		t.Errorf("CREATE DATABASE not filtered:\n%s", got)
	}
	if !strings.Contains(string(got), "CREATE TABLE wp_users") {
		t.Errorf("table line missing:\n%s", got)
	}
}

func TestPrepareDump_GzipDecompresses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.sql.gz")
	body := "-- mysqldump\nCREATE TABLE x (id INT);\n"
	gzWriteFile(t, src, []byte(body))
	dst := filepath.Join(dir, "out.sql")
	if err := prepareDump(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !strings.Contains(string(got), "CREATE TABLE x") {
		t.Errorf("gzip pipeline broke:\n%s", got)
	}
}

func TestPrepareDump_ZipSingleSQL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.zip")
	zipOne(t, src, "dump.sql", []byte("-- header\nINSERT INTO wp_posts VALUES (1);\n"))
	dst := filepath.Join(dir, "out.sql")
	if err := prepareDump(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !strings.Contains(string(got), "INSERT INTO wp_posts") {
		t.Errorf("zip pipeline broke:\n%s", got)
	}
}

func TestPrepareDump_RejectsXZ(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.sql.xz")
	mustWrite(t, src, []byte("garbage"))
	err := prepareDump(src, filepath.Join(dir, "out.sql"))
	if err == nil {
		t.Fatal("expected unsupported error for .xz")
	}
	if !strings.Contains(err.Error(), "decompress") {
		t.Errorf("error should explain decompress workaround: %v", err)
	}
}

func TestPrepareDump_RejectsNonSQL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "binary.dat")
	mustWrite(t, src, []byte("\x00\x01\x02 binary nonsense"))
	if err := prepareDump(src, filepath.Join(dir, "out.sql")); err == nil {
		t.Fatal("expected non-SQL rejection")
	}
}

func TestPrepareDump_AtomicNoPartialDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "garbage.sql")
	mustWrite(t, src, []byte("\x00\x01")) // looksLikeSQL → false → error mid-pipeline
	dst := filepath.Join(dir, "out.sql")
	if err := prepareDump(src, dst); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("dst should not exist after failed prepare, got err=%v", err)
	}
	// And no leftover temps in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".locorum-import-") {
			t.Errorf("leftover tmpfile: %s", e.Name())
		}
	}
}

// helpers

func mustWrite(t *testing.T, p string, body []byte) {
	t.Helper()
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func gzWriteFile(t *testing.T, p string, body []byte) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := gzip.NewWriter(f)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func zipOne(t *testing.T, p, entryName string, body []byte) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(w, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}
