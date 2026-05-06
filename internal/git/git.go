// Package git wraps the user's installed git CLI for the small set of
// operations Locorum needs: cloning a remote, fetching, listing
// branches, creating and removing worktrees, and detecting dirty
// state. We shell out to git rather than using go-git so the user's
// own .gitconfig (auth helpers, signing keys, signed pushes, identity)
// applies transparently — exactly what an agent running locally would
// expect.
//
// Every function takes a context.Context. Cancellation is honoured
// via exec.CommandContext, which sends SIGKILL on cancel; that's
// blunt but appropriate for a multi-minute clone the user wants to
// abort.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner abstracts command execution so tests can swap in a fake.
// Default is the OS git binary.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (string, string, error)
}

// CLI is the production Runner. dir is the repository root (or empty
// for global commands like `git clone <url> <dest>`); args are passed
// verbatim to git.
type CLI struct{}

// Run executes git with args in dir and returns its stdout and stderr.
// Non-zero exit codes are surfaced as a *ExitError so callers can
// distinguish "git refused" from "git wasn't found."
func (CLI) Run(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// Default is the package-wide singleton used by Clone / Fetch / etc.
// Tests swap it via WithRunner; production never reassigns.
var Default Runner = CLI{}

// withRunner returns r if non-nil, else Default. Lets tests inject a
// fake without forcing a global swap.
func withRunner(r Runner) Runner {
	if r == nil {
		return Default
	}
	return r
}

// ExitError is returned when git ran but reported a non-zero exit.
// Stderr captures git's complaint so callers can show it verbatim.
type ExitError struct {
	Args   []string
	Dir    string
	Stderr string
	Cause  error
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	stderr := strings.TrimSpace(e.Stderr)
	if stderr != "" {
		return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), stderr)
	}
	return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Cause)
}

func (e *ExitError) Unwrap() error { return e.Cause }

// run is the package-internal helper that wraps every git invocation
// in a uniform error envelope.
func run(ctx context.Context, r Runner, dir string, args ...string) (string, error) {
	stdout, stderr, err := withRunner(r).Run(ctx, dir, args...)
	if err != nil {
		return stdout, &ExitError{Args: args, Dir: dir, Stderr: stderr, Cause: err}
	}
	return stdout, nil
}

// Clone shallow-clones remote into dest. Idempotent: if dest already
// exists and is a git repo, Clone is a no-op (fetch via Fetch).
//
// We use --depth=1 by default to keep clones fast; agents that need
// full history can pass extra args via CloneOptions.
type CloneOptions struct {
	// Runner overrides the default git CLI runner. Tests inject; nil
	// in production.
	Runner Runner

	// Depth, when > 0, passes --depth N. Zero means full history.
	Depth int

	// Branch, when set, passes --branch B and clones only that ref.
	// Useful when the user knows up-front which branch they want.
	Branch string
}

// Clone runs `git clone` from a clean dest path. Returns
// errors.New("destination is not empty") if dest already contains a
// non-git directory; callers that want a fetch-or-clone should call
// EnsureRepo instead.
func Clone(ctx context.Context, remote, dest string, opts CloneOptions) error {
	args := []string{"clone"}
	if opts.Depth > 0 {
		args = append(args, "--depth", fmt.Sprintf("%d", opts.Depth))
	}
	if opts.Branch != "" {
		args = append(args, "--branch", opts.Branch)
	}
	args = append(args, remote, dest)
	_, err := run(ctx, opts.Runner, "", args...)
	return err
}

// Fetch updates remotes for an existing repo. Equivalent to running
// `git fetch --all --prune` from inside the working tree.
func Fetch(ctx context.Context, repoDir string, r Runner) error {
	_, err := run(ctx, r, repoDir, "fetch", "--all", "--prune")
	return err
}

// IsRepo reports whether dir is a git working tree. Cheap (single
// `rev-parse` call) but allocates a process — callers should cache the
// result if they're calling it in a hot loop.
func IsRepo(ctx context.Context, dir string, r Runner) bool {
	_, err := run(ctx, r, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// EnsureRepo clones remote into dest if dest is empty / missing,
// otherwise runs Fetch on the existing repo. Returns the absolute
// repo path on success. Concurrent calls for the same dest race at
// the filesystem level — callers serialise via the per-site mutex.
func EnsureRepo(ctx context.Context, remote, dest string, opts CloneOptions) (string, error) {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("resolve dest: %w", err)
	}
	if IsRepo(ctx, abs, opts.Runner) {
		if err := Fetch(ctx, abs, opts.Runner); err != nil {
			return abs, err
		}
		return abs, nil
	}
	if err := Clone(ctx, remote, abs, opts); err != nil {
		return abs, err
	}
	return abs, nil
}

// WorktreeAdd creates a worktree at path tracking branch. If branch
// does not exist locally, --track is used to create it from
// origin/<branch>. Failures during creation surface git's stderr.
func WorktreeAdd(ctx context.Context, repoDir, path, branch string, r Runner) error {
	if path == "" || branch == "" {
		return errors.New("WorktreeAdd: path and branch are required")
	}
	// Existence check first so we don't fight a stale .git/worktrees
	// entry. -f --force lets us reattach when git has the entry but
	// the path is missing.
	if branchExists(ctx, repoDir, branch, r) {
		_, err := run(ctx, r, repoDir, "worktree", "add", path, branch)
		return err
	}
	// Create the branch from origin/<branch>. If origin doesn't have
	// it either, git will fail with a clear message that we forward.
	_, err := run(ctx, r, repoDir, "worktree", "add", "-b", branch, path, "origin/"+branch)
	return err
}

// WorktreeRemove removes the worktree at path. force=true uses
// `--force` so dirty trees don't block removal — appropriate when the
// caller has confirmed the user wants to discard changes.
func WorktreeRemove(ctx context.Context, repoDir, path string, force bool, r Runner) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(ctx, r, repoDir, args...)
	return err
}

// HasUncommittedChanges reports whether the worktree at dir has any
// staged, unstaged, or untracked changes. Used by DeleteSite to warn
// before removing a worktree.
func HasUncommittedChanges(ctx context.Context, dir string, r Runner) (bool, error) {
	out, err := run(ctx, r, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// branchExists reports whether the given branch is present locally
// (refs/heads/<branch>). Used by WorktreeAdd to decide between
// `worktree add <path> <branch>` and `worktree add -b <branch>`.
func branchExists(ctx context.Context, repoDir, branch string, r Runner) bool {
	_, err := run(ctx, r, repoDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}
