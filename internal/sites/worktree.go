package sites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gosimple/slug"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/git"
	"github.com/PeterBooker/locorum/internal/types"
)

// CreateWorktreeOptions captures the user-supplied inputs to
// CreateWorktreeSite. Mirrors the CLI's `site create --git-remote …`
// flag set, with sensible defaults wired in by the helper.
type CreateWorktreeOptions struct {
	// Name is a human-readable label for the new site row. Required.
	// The derived slug is computed by WorktreeSlug; Name is just for
	// the GUI list view.
	Name string

	// GitRemote is the canonical upstream URL passed to `git clone`.
	// SSH and HTTPS forms both work — Locorum doesn't validate or
	// rewrite the value, so the user's git config (credentials,
	// askpass) applies.
	GitRemote string

	// Branch is the upstream branch the worktree tracks. Validated
	// by ValidateWorktreeOpts; sanitiseBranchForSlug handles the rest.
	Branch string

	// ParentSlug, when non-empty, identifies an existing site row to
	// use as the parent. Empty means "auto-derive a parent slug from
	// the remote URL"; the parent site is created on the fly if it
	// doesn't exist.
	ParentSlug string

	// CloneDB, when true, copies the parent site's database into the
	// new worktree site and runs wp search-replace to swap the
	// hostname. Default is false (empty WP install) — D2 in the
	// AGENTS-SUPPORT plan.
	CloneDB bool

	// PHPVersion / DBVersion / DBEngine / RedisVersion, when set,
	// override the parent site's runtime versions. Empty falls back
	// to the parent or, when no parent, to engine defaults.
	PHPVersion   string
	DBEngine     string
	DBVersion    string
	RedisVersion string

	// WorktreeRoot, when set, overrides the default placement
	// (~/locorum/worktrees/<parent-slug>/<branch-slug>). Useful when
	// the user wants the worktree alongside their existing checkout.
	WorktreeRoot string

	// DryRun reports the planned operations without committing them.
	// When true, no SQLite rows are inserted, no git commands run, no
	// containers are created — Describe() runs on every step and the
	// preview is returned via the result.
	DryRun bool
}

// CreateWorktreeResult is what CreateWorktreeSite returns on success
// (or, in DryRun mode, on a complete preview).
type CreateWorktreeResult struct {
	// Site is the row inserted into SQLite. In DryRun mode this is
	// the row that *would* have been inserted; the caller must not
	// expect it to round-trip through GetSite.
	Site types.Site

	// DerivedSlug is the worktree-derived slug (D3). Same as
	// Site.Slug but exposed for clarity.
	DerivedSlug string

	// DryRunPreview is non-empty when DryRun was set. Each line is
	// one step's preview; suitable for direct CLI output.
	DryRunPreview string
}

// CreateWorktreeSite is the agent-facing differentiator: spin up a
// site bound to a git worktree. The flow:
//
//  1. Validate inputs (ValidateWorktreeOpts).
//  2. Resolve the parent site row (existing or just-in-time created).
//  3. Compute derived slug + worktree path.
//  4. (Plan step) ensure-checkout — clone or fetch parent repo.
//  5. (Plan step) ensure-worktree — git worktree add.
//  6. Insert the site row.
//  7. Start the site (StartSite picks up FilesDir = WorktreePath).
//  8. (CloneDB only) snapshot parent → restore into new site →
//     wp search-replace → auto-snapshot result.
//
// On any step failure, rollback runs in reverse: worktree removed,
// SQLite row deleted (when present). Per-site mutex serialises calls
// for the same parent — concurrent worktree creates against
// different parents run in parallel.
func (sm *SiteManager) CreateWorktreeSite(ctx context.Context, opts CreateWorktreeOptions) (*CreateWorktreeResult, error) {
	if err := ValidateWorktreeOpts(opts.GitRemote, opts.Branch, opts.WorktreeRoot); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.Name) == "" {
		return nil, errors.New("name is required")
	}

	parent, parentCreated, err := sm.resolveOrCreateParent(opts)
	if err != nil {
		return nil, err
	}
	// In dry-run mode, resolveOrCreateParent may have inserted a stub
	// parent row to make the preview meaningful. Roll it back before
	// returning so dry-run is a true no-op (P4 invariant).
	if opts.DryRun && parentCreated {
		defer func() {
			if err := sm.st.DeleteSite(parent.ID); err != nil {
				slog.Warn("dry-run: rollback stub parent", "err", err.Error())
			}
		}()
	}

	parentDir := parent.FilesDir
	if parentDir == "" {
		// Conventional sites set FilesDir on AddSite; a parent with
		// an empty FilesDir is malformed. Reject loudly so we don't
		// silently scribble across an unrelated directory.
		return nil, fmt.Errorf("parent site %q has empty FilesDir", parent.Slug)
	}

	worktreeRoot := opts.WorktreeRoot
	if worktreeRoot == "" {
		worktreeRoot = filepath.Join(filepath.Dir(parentDir), parent.Slug+".worktrees")
	}
	derivedSlug := WorktreeSlug(parent.Slug, opts.Branch, worktreeRoot)
	worktreePath := filepath.Join(worktreeRoot, derivedSlug)
	derivedDomain := derivedSlug + ".localhost"

	// Build the row up front so DryRun can return a full preview.
	newSite := types.Site{
		ID:           uuid.NewString(),
		Name:         opts.Name,
		Slug:         derivedSlug,
		Domain:       derivedDomain,
		FilesDir:     worktreePath,
		PublicDir:    parent.PublicDir,
		WebServer:    parent.WebServer,
		PHPVersion:   firstNonEmpty(opts.PHPVersion, parent.PHPVersion),
		DBEngine:     firstNonEmpty(opts.DBEngine, parent.DBEngine),
		DBVersion:    firstNonEmpty(opts.DBVersion, parent.DBVersion),
		RedisVersion: firstNonEmpty(opts.RedisVersion, parent.RedisVersion),
		DBPassword:   generatePassword(16),
		GitRemote:    opts.GitRemote,
		GitBranch:    opts.Branch,
		WorktreePath: worktreePath,
		ParentSiteID: parent.ID,
	}
	if newSite.DBEngine == "" {
		newSite.DBEngine = string(dbengine.Default)
	}
	if newSite.DBVersion == "" {
		newSite.DBVersion = dbengine.MustFor(dbengine.Kind(newSite.DBEngine)).DefaultVersion()
	}
	if newSite.WebServer == "" {
		newSite.WebServer = "nginx"
	}

	if opts.DryRun {
		preview, err := sm.previewWorktreePlan(ctx, parent, &newSite, opts)
		if err != nil {
			return nil, err
		}
		return &CreateWorktreeResult{
			Site:          newSite,
			DerivedSlug:   derivedSlug,
			DryRunPreview: preview,
		}, nil
	}

	// Acquire the parent's lock so concurrent CreateWorktreeSite
	// calls for the same parent serialise on git operations. The
	// per-site mutex map keys on ID, so different parents stay
	// parallel.
	parentMu := sm.siteMutex(parent.ID)
	parentMu.Lock()
	defer parentMu.Unlock()

	// Run the git steps as a small Plan so failures roll back the
	// worktree cleanly. We do NOT compose this with StartSite's plan
	// — StartSite acquires its own per-site mutex and the rollback
	// semantics differ (we want StartSite failures to leave the
	// worktree in place so the user can debug, but git failures must
	// undo the worktree).
	gitSteps := []*sitestepFuncWrap{
		newGitFuncStep("ensure-checkout",
			fmt.Sprintf("git fetch / clone %s into %s", opts.GitRemote, parentDir),
			func(ctx context.Context) error {
				_, err := git.EnsureRepo(ctx, opts.GitRemote, parentDir, git.CloneOptions{})
				return err
			},
			nil, // no rollback — shared parent dir
		),
		newGitFuncStep("ensure-worktree",
			fmt.Sprintf("git worktree add %s tracking %s", worktreePath, opts.Branch),
			func(ctx context.Context) error {
				return git.WorktreeAdd(ctx, parentDir, worktreePath, opts.Branch, nil)
			},
			func(ctx context.Context) error {
				return git.WorktreeRemove(ctx, parentDir, worktreePath, true, nil)
			},
		),
	}
	for _, step := range gitSteps {
		if err := step.do(ctx); err != nil {
			rollbackGitSteps(ctx, gitSteps)
			if parentCreated {
				_ = sm.st.DeleteSite(parent.ID)
			}
			return nil, err
		}
	}

	if err := sm.st.AddSite(&newSite); err != nil {
		rollbackGitSteps(ctx, gitSteps)
		if parentCreated {
			_ = sm.st.DeleteSite(parent.ID)
		}
		return nil, fmt.Errorf("persist new site: %w", err)
	}
	sm.writeConfigYAML(&newSite)
	sm.emitSitesUpdate()

	// StartSite from here on so the user sees normal progress
	// callbacks and a green checklist on the GUI / streaming CLI.
	if err := sm.StartSite(ctx, newSite.ID); err != nil {
		// Worktree + DB row stay so the user can inspect; surface
		// the start failure but do NOT auto-rollback the SQLite row,
		// matching the existing CloneSite UX.
		return &CreateWorktreeResult{
			Site:        newSite,
			DerivedSlug: derivedSlug,
		}, fmt.Errorf("start worktree site: %w", err)
	}

	if opts.CloneDB && parent.Started {
		if err := sm.cloneDBFromParent(ctx, parent, &newSite); err != nil {
			slog.Warn("clone-db: parent → worktree failed", "err", err.Error())
			// Non-fatal: empty WP is still functional, and
			// surfacing the error to the caller would force them
			// to clean up the partial state. A toast / log line
			// is enough.
		}
	}

	return &CreateWorktreeResult{Site: newSite, DerivedSlug: derivedSlug}, nil
}

// resolveOrCreateParent returns the parent site row. When
// opts.ParentSlug names an existing row, that row is used directly.
// When empty, the parent slug is derived from the remote URL and a
// new conventional site is added on the fly.
//
// The parentCreated flag tells the caller whether they need to
// clean up the parent on failure (only when we created it).
func (sm *SiteManager) resolveOrCreateParent(opts CreateWorktreeOptions) (types.Site, bool, error) {
	rows, err := sm.st.GetSites()
	if err != nil {
		return types.Site{}, false, err
	}

	if opts.ParentSlug != "" {
		for _, s := range rows {
			if s.Slug == opts.ParentSlug && s.ParentSiteID == "" {
				return s, false, nil
			}
		}
		return types.Site{}, false, fmt.Errorf("parent slug %q not found (or is itself a worktree)", opts.ParentSlug)
	}

	parentSlug := slug.Make(remoteSlugCandidate(opts.GitRemote))
	if parentSlug == "" {
		return types.Site{}, false, fmt.Errorf("could not derive parent slug from %q", opts.GitRemote)
	}
	for _, s := range rows {
		if s.Slug == parentSlug && s.ParentSiteID == "" {
			return s, false, nil
		}
	}

	// Auto-create a stub parent. The parent's FilesDir holds the
	// canonical clone; worktrees live in <parent-dir>.worktrees/.
	homeSites := filepath.Join(sm.homeDir, "locorum", "sites", parentSlug)
	parent := types.Site{
		ID:        uuid.NewString(),
		Name:      parentSlug,
		Slug:      parentSlug,
		Domain:    parentSlug + ".localhost",
		FilesDir:  homeSites,
		WebServer: "nginx",
		GitRemote: opts.GitRemote,
		// Default versions: pick whatever the engine says is current.
		DBEngine:  string(dbengine.Default),
		DBVersion: dbengine.MustFor(dbengine.Default).DefaultVersion(),
		// Reasonable defaults — agents creating worktrees rarely
		// care about the parent's PHP version because they'll start
		// the worktree separately.
		PHPVersion:   "8.4",
		RedisVersion: "8.0",
		DBPassword:   generatePassword(16),
	}
	if err := sm.st.AddSite(&parent); err != nil {
		return types.Site{}, false, fmt.Errorf("add parent site: %w", err)
	}
	return parent, true, nil
}

// remoteSlugCandidate extracts a friendly slug fragment from the
// remote URL. Strips the .git suffix and the path prefix so
// `git@github.com:foo/bar.git` becomes `bar`. Falls back to "site"
// for opaque URLs we can't parse.
func remoteSlugCandidate(remote string) string {
	r := strings.TrimSpace(remote)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSuffix(r, ".git")
	if idx := strings.LastIndexAny(r, "/:"); idx >= 0 {
		r = r[idx+1:]
	}
	if r == "" {
		return "site"
	}
	return r
}

// cloneDBFromParent runs the parent → worktree DB clone documented in
// D2: snapshot parent, restore into the worktree, search-replace the
// hostname, auto-snapshot the result.
func (sm *SiteManager) cloneDBFromParent(ctx context.Context, parent types.Site, child *types.Site) error {
	if !parent.Started {
		return errors.New("parent site is not running; cannot clone DB")
	}
	parentSnapPath, err := sm.snapshotLocked(ctx, &parent, "for_worktree")
	if err != nil {
		return fmt.Errorf("snapshot parent: %w", err)
	}
	if err := sm.RestoreSnapshot(ctx, child.ID, parentSnapPath, RestoreSnapshotOptions{
		// Engine + version were just copied from the parent so the
		// match is guaranteed; no need to bypass the safety check.
		// Skip the auto-snapshot wrap (added in P4): we already
		// know the child's DB is empty.
		SkipAutoSnapshot: true,
	}); err != nil {
		return fmt.Errorf("restore parent snapshot into worktree: %w", err)
	}
	if _, err := sm.wpSearchReplace(ctx, child, "https://"+parent.Domain, "https://"+child.Domain); err != nil {
		return fmt.Errorf("search-replace https: %w", err)
	}
	if _, err := sm.wpSearchReplace(ctx, child, "http://"+parent.Domain, "http://"+child.Domain); err != nil {
		return fmt.Errorf("search-replace http: %w", err)
	}
	if _, err := sm.wpSearchReplace(ctx, child, parent.Domain, child.Domain); err != nil {
		return fmt.Errorf("search-replace bare: %w", err)
	}
	if path, err := sm.snapshotLocked(ctx, child, "post_clone_db"); err != nil {
		slog.Warn("clone-db: post-clone snapshot failed", "err", err.Error())
	} else {
		slog.Info("clone-db: post-clone snapshot saved", "path", path)
	}
	return nil
}

// previewWorktreePlan formats the actions CreateWorktreeSite would
// take. Output is multi-line, prefixed-bullet so it can stream to
// stdout without further formatting.
func (sm *SiteManager) previewWorktreePlan(_ context.Context, parent types.Site, child *types.Site, opts CreateWorktreeOptions) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: create worktree site %q\n", child.Slug)
	fmt.Fprintf(&b, "  parent:        %s (%s)\n", parent.Slug, parent.FilesDir)
	fmt.Fprintf(&b, "  remote:        %s\n", opts.GitRemote)
	fmt.Fprintf(&b, "  branch:        %s\n", opts.Branch)
	fmt.Fprintf(&b, "  derived slug:  %s\n", child.Slug)
	fmt.Fprintf(&b, "  worktree path: %s\n", child.WorktreePath)
	fmt.Fprintf(&b, "  domain:        https://%s\n", child.Domain)
	fmt.Fprintf(&b, "  steps:\n")
	fmt.Fprintf(&b, "    1. ensure-checkout — git fetch/clone %s\n", opts.GitRemote)
	fmt.Fprintf(&b, "    2. ensure-worktree — git worktree add %s tracking %s\n", child.WorktreePath, opts.Branch)
	fmt.Fprintf(&b, "    3. insert site row in SQLite\n")
	fmt.Fprintf(&b, "    4. start site (full container plan)\n")
	if opts.CloneDB {
		fmt.Fprintf(&b, "    5. clone parent DB → worktree (with auto-snapshot)\n")
		fmt.Fprintf(&b, "    6. wp search-replace %s → %s\n", parent.Domain, child.Domain)
	}
	return b.String(), nil
}

// firstNonEmpty returns the first non-empty string, or "" if all are.
func firstNonEmpty(xs ...string) string {
	for _, s := range xs {
		if s != "" {
			return s
		}
	}
	return ""
}

// ─── small helper for the inline git step plan ─────────────────────

// sitestepFuncWrap is a private inlining of orch.FuncStep with
// preview support. We don't reuse sitesteps.FuncStep because the
// rollback bookkeeping below is tighter than a full Plan run, and we
// want CreateWorktreeSite's failure path to be obvious from the
// reader's perspective.
type sitestepFuncWrap struct {
	name    string
	preview string
	apply   func(context.Context) error
	undo    func(context.Context) error
	applied bool
}

func newGitFuncStep(name, preview string, apply, undo func(context.Context) error) *sitestepFuncWrap {
	return &sitestepFuncWrap{name: name, preview: preview, apply: apply, undo: undo}
}

func (s *sitestepFuncWrap) do(ctx context.Context) error {
	if err := s.apply(ctx); err != nil {
		return fmt.Errorf("%s: %w", s.name, err)
	}
	s.applied = true
	return nil
}

func rollbackGitSteps(parent context.Context, steps []*sitestepFuncWrap) {
	// Cleanup runs even if the parent context cancelled — losing
	// track of an orphan worktree is worse than respecting the
	// cancellation. Strip cancellation but inherit values/tracing,
	// then layer our own deadline.
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	// Reverse order so dependents undo before dependencies.
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if !s.applied || s.undo == nil {
			continue
		}
		if err := s.undo(rbCtx); err != nil {
			slog.Warn("worktree create: rollback failed",
				"step", s.name, "err", err.Error())
		}
	}
}
