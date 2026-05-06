package sitesteps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/git"
	"github.com/PeterBooker/locorum/internal/orch"
)

// Worktree-related Plan steps used by SiteManager.CreateWorktreeSite.
// All of these implement orch.Describer so a `--dry-run` invocation
// can preview their effect without touching the filesystem or network.

// EnsureCheckoutStep clones the parent repository into ParentDir if
// missing, otherwise runs `git fetch --all --prune` to keep refs
// fresh. Idempotent: repeating after success is cheap and never
// destroys local commits.
//
// Rollback is a no-op: the parent checkout is shared by every
// worktree-bound site for the same repo, and one failed CreateSite
// must not delete it for the others.
type EnsureCheckoutStep struct {
	Remote    string
	ParentDir string
	Runner    git.Runner // nil means git.Default (production)
}

func (s *EnsureCheckoutStep) Name() string { return "ensure-checkout" }

func (s *EnsureCheckoutStep) Apply(ctx context.Context) error {
	if s.Remote == "" || s.ParentDir == "" {
		return fmt.Errorf("ensure-checkout: Remote and ParentDir are required")
	}
	if err := os.MkdirAll(filepath.Dir(s.ParentDir), 0o755); err != nil {
		return fmt.Errorf("ensure-checkout: parent dir: %w", err)
	}
	_, err := git.EnsureRepo(ctx, s.Remote, s.ParentDir, git.CloneOptions{Runner: s.Runner})
	return err
}

func (s *EnsureCheckoutStep) Rollback(_ context.Context) error { return nil }

func (s *EnsureCheckoutStep) Describe(_ context.Context) (string, error) {
	if s.Remote == "" {
		return "(no-op — remote unset)", nil
	}
	if _, err := os.Stat(filepath.Join(s.ParentDir, ".git")); err == nil {
		return fmt.Sprintf("git fetch in %s (existing repo)", s.ParentDir), nil
	}
	return fmt.Sprintf("git clone %s into %s", s.Remote, s.ParentDir), nil
}

// EnsureWorktreeStep adds a git worktree for Branch at WorktreePath.
// On rollback, removes the worktree (with --force if AllowForce is
// set) so a half-set-up site does not leave a dangling git
// administrative entry behind.
//
// Branch is created from origin/<branch> if it doesn't exist locally.
// That mirrors how `git worktree add -b X path origin/X` behaves and
// keeps the agent's "create a worktree for an upstream branch" flow
// trivially correct.
type EnsureWorktreeStep struct {
	ParentDir    string
	WorktreePath string
	Branch       string
	Runner       git.Runner
}

func (s *EnsureWorktreeStep) Name() string { return "ensure-worktree" }

func (s *EnsureWorktreeStep) Apply(ctx context.Context) error {
	if s.ParentDir == "" || s.WorktreePath == "" || s.Branch == "" {
		return fmt.Errorf("ensure-worktree: ParentDir, WorktreePath, Branch required")
	}
	// If the worktree path already contains a working tree, the
	// caller already created it (e.g. partial recovery from a previous
	// run). git worktree add fails in that case — short-circuit.
	if _, err := os.Stat(filepath.Join(s.WorktreePath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.WorktreePath), 0o755); err != nil {
		return fmt.Errorf("ensure-worktree: parent dir: %w", err)
	}
	return git.WorktreeAdd(ctx, s.ParentDir, s.WorktreePath, s.Branch, s.Runner)
}

func (s *EnsureWorktreeStep) Rollback(ctx context.Context) error {
	if s.ParentDir == "" || s.WorktreePath == "" {
		return nil
	}
	// Force on rollback: if the user's flow died after we created the
	// worktree but before they did anything with it, there's no work
	// to preserve.
	return git.WorktreeRemove(ctx, s.ParentDir, s.WorktreePath, true, s.Runner)
}

func (s *EnsureWorktreeStep) Describe(_ context.Context) (string, error) {
	return fmt.Sprintf("git worktree add %s tracking %s in %s",
		s.WorktreePath, s.Branch, s.ParentDir), nil
}

// RemoveWorktreeStep tears down a git worktree as part of a delete
// plan. Force is set on rollback by the caller; the typical user-
// facing path consults DiscardChanges first.
type RemoveWorktreeStep struct {
	ParentDir    string
	WorktreePath string
	Force        bool
	Runner       git.Runner
}

func (s *RemoveWorktreeStep) Name() string { return "remove-worktree" }

func (s *RemoveWorktreeStep) Apply(ctx context.Context) error {
	if s.ParentDir == "" || s.WorktreePath == "" {
		return nil
	}
	// Tolerate "already removed": git returns nonzero with a clear
	// message when the worktree directory is missing. Stat and skip
	// keeps Apply idempotent.
	if _, err := os.Stat(s.WorktreePath); os.IsNotExist(err) {
		return nil
	}
	return git.WorktreeRemove(ctx, s.ParentDir, s.WorktreePath, s.Force, s.Runner)
}

func (s *RemoveWorktreeStep) Rollback(_ context.Context) error {
	// No undo for a worktree removal: re-creating would not restore
	// the original work. We accept the asymmetry — Apply only runs
	// inside a delete plan.
	return nil
}

func (s *RemoveWorktreeStep) Describe(_ context.Context) (string, error) {
	flag := ""
	if s.Force {
		flag = " --force"
	}
	return fmt.Sprintf("git worktree remove%s %s", flag, s.WorktreePath), nil
}

// Compile-time guards.
var (
	_ orch.Step      = (*EnsureCheckoutStep)(nil)
	_ orch.Describer = (*EnsureCheckoutStep)(nil)
	_ orch.Step      = (*EnsureWorktreeStep)(nil)
	_ orch.Describer = (*EnsureWorktreeStep)(nil)
	_ orch.Step      = (*RemoveWorktreeStep)(nil)
	_ orch.Describer = (*RemoveWorktreeStep)(nil)
)
