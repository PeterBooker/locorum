package git

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// fakeRunner records every invocation and returns scripted responses.
// Tests assert against the recorded args to verify that the helpers
// build the right git command lines.
type fakeRunner struct {
	calls []recordedCall
	// responses is keyed by the joined args; missing keys return
	// empty stdout and nil error.
	responses map[string]response
}

type recordedCall struct {
	dir  string
	args []string
}

type response struct {
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) Run(_ context.Context, dir string, args ...string) (string, string, error) {
	f.calls = append(f.calls, recordedCall{dir: dir, args: append([]string{}, args...)})
	if f.responses == nil {
		return "", "", nil
	}
	key := joinArgs(args)
	r, ok := f.responses[key]
	if !ok {
		return "", "", nil
	}
	return r.stdout, r.stderr, r.err
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func TestClone_Args(t *testing.T) {
	r := &fakeRunner{}
	if err := Clone(context.Background(), "git@x:repo.git", "/tmp/dest", CloneOptions{
		Runner: r, Depth: 1, Branch: "main",
	}); err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	args := r.calls[0].args
	want := []string{"clone", "--depth", "1", "--branch", "main", "git@x:repo.git", "/tmp/dest"}
	if joinArgs(args) != joinArgs(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestEnsureRepo_FetchPathOnExistingRepo(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"rev-parse --is-inside-work-tree": {stdout: "true\n"},
		},
	}
	abs, _ := filepath.Abs("/tmp/repo")
	dest, err := EnsureRepo(context.Background(), "git@x:repo.git", abs, CloneOptions{Runner: r})
	if err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if dest != abs {
		t.Fatalf("dest mismatch: %q vs %q", dest, abs)
	}
	// Calls: rev-parse, then fetch.
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d (%v)", len(r.calls), r.calls)
	}
	if r.calls[1].args[0] != "fetch" {
		t.Fatalf("expected fetch as second call, got %v", r.calls[1].args)
	}
}

func TestEnsureRepo_ClonesWhenMissing(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"rev-parse --is-inside-work-tree": {err: errors.New("not a git dir")},
		},
	}
	abs, _ := filepath.Abs("/tmp/new-repo")
	if _, err := EnsureRepo(context.Background(), "git@x:repo.git", abs, CloneOptions{Runner: r}); err != nil {
		t.Fatalf("EnsureRepo: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d (%v)", len(r.calls), r.calls)
	}
	if r.calls[1].args[0] != "clone" {
		t.Fatalf("expected clone as second call, got %v", r.calls[1].args)
	}
}

func TestWorktreeAdd_BranchExists(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"rev-parse --verify --quiet refs/heads/feature": {stdout: "abc123\n"},
		},
	}
	if err := WorktreeAdd(context.Background(), "/repo", "/tmp/wt", "feature", r); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	last := r.calls[len(r.calls)-1].args
	want := []string{"worktree", "add", "/tmp/wt", "feature"}
	if joinArgs(last) != joinArgs(want) {
		t.Fatalf("args = %v, want %v", last, want)
	}
}

func TestWorktreeAdd_BranchMissingTracksOrigin(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"rev-parse --verify --quiet refs/heads/feature": {err: errors.New("no")},
		},
	}
	if err := WorktreeAdd(context.Background(), "/repo", "/tmp/wt", "feature", r); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	last := r.calls[len(r.calls)-1].args
	want := []string{"worktree", "add", "-b", "feature", "/tmp/wt", "origin/feature"}
	if joinArgs(last) != joinArgs(want) {
		t.Fatalf("args = %v, want %v", last, want)
	}
}

func TestWorktreeRemove_Force(t *testing.T) {
	r := &fakeRunner{}
	if err := WorktreeRemove(context.Background(), "/repo", "/tmp/wt", true, r); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	args := r.calls[0].args
	want := []string{"worktree", "remove", "--force", "/tmp/wt"}
	if joinArgs(args) != joinArgs(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestHasUncommittedChanges_Empty(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"status --porcelain": {stdout: ""},
		},
	}
	dirty, err := HasUncommittedChanges(context.Background(), "/repo", r)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if dirty {
		t.Fatalf("expected clean tree")
	}
}

func TestHasUncommittedChanges_Dirty(t *testing.T) {
	r := &fakeRunner{
		responses: map[string]response{
			"status --porcelain": {stdout: " M README.md\n"},
		},
	}
	dirty, err := HasUncommittedChanges(context.Background(), "/repo", r)
	if err != nil {
		t.Fatalf("HasUncommittedChanges: %v", err)
	}
	if !dirty {
		t.Fatalf("expected dirty tree")
	}
}
