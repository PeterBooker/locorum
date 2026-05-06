package daemon

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquire_FreshLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.lock")

	lock, err := Acquire(path, "test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	got := lock.Owner()
	if got.PID == 0 {
		t.Fatalf("owner pid not stamped")
	}
	if got.Version != "test" {
		t.Fatalf("version: got %q want %q", got.Version, "test")
	}
}

func TestAcquire_SecondCallerIsRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.lock")

	lock, err := Acquire(path, "first")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lock.Release()

	_, err = Acquire(path, "second")
	if err == nil {
		t.Fatalf("expected ErrLocked, got nil")
	}
	var locked *ErrLocked
	if !errors.As(err, &locked) {
		t.Fatalf("expected *ErrLocked, got %T: %v", err, err)
	}
	if locked.Owner.PID == 0 {
		t.Fatalf("ErrLocked: owner pid not populated")
	}
}

func TestAcquire_AfterReleaseSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.lock")

	first, err := Acquire(path, "first")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := Acquire(path, "second")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer second.Release()
	if second.Owner().Version != "second" {
		t.Fatalf("second owner.version: got %q", second.Owner().Version)
	}
}

func TestReadOwner_MissingReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.lock")

	got, err := ReadOwner(path)
	if err != nil {
		t.Fatalf("ReadOwner: %v", err)
	}
	if got.PID != 0 {
		t.Fatalf("expected zero owner, got %+v", got)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owner.lock")
	lock, err := Acquire(path, "test")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release should be no-op: %v", err)
	}
}
