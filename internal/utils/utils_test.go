package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDir_CreatesNew(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected a directory")
	}
}

func TestEnsureDir_ExistingDir(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
}

func TestEnsureDir_FileConflict(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	err := EnsureDir(f)
	if err == nil {
		t.Fatal("expected error when path is a file")
	}
}

func TestEnsureDir_NestedDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")

	if err := EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected a directory")
	}
}
