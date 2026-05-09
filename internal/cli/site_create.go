package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/sites"
)

// runSiteCreate dispatches `locorum site create`. v1 supports the
// worktree flow (--git-remote required); a future revision will add
// blank-WP creation matching the GUI's New Site modal. For now,
// blank-WP is a GUI-only path because it requires file-system pickers
// the CLI can't reproduce cleanly.
func runSiteCreate(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("site create", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	name := fs.String("name", "", "human-readable site name (required)")
	gitRemote := fs.String("git-remote", "", "upstream git URL (required for v1)")
	branch := fs.String("branch", "", "branch to track (required when --git-remote is set)")
	parent := fs.String("parent-slug", "", "existing parent site slug (else auto-derived from --git-remote)")
	cloneDB := fs.Bool("clone-db", false, "copy parent's DB and run search-replace")
	dryRun := fs.Bool("dry-run", false, "describe the plan without executing")
	php := fs.String("php", "", "PHP version override")
	dbEngine := fs.String("db-engine", "", "DB engine override (mysql|mariadb)")
	dbVersion := fs.String("db-version", "", "DB version override")
	redis := fs.String("redis", "", "Redis version override")
	worktreeRoot := fs.String("worktree-root", "", "host path for the worktree directory (defaults to <parent>.worktrees/)")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if strings.TrimSpace(*name) == "" || strings.TrimSpace(*gitRemote) == "" || strings.TrimSpace(*branch) == "" {
		_, _ = fmt.Fprintln(env.Stderr, "usage: locorum site create --name N --git-remote URL --branch B [--clone-db] [--dry-run]")
		return ExitUsage
	}

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer func() { _ = cli.Close() }()

	params := map[string]any{
		"name":         *name,
		"gitRemote":    *gitRemote,
		"branch":       *branch,
		"parentSlug":   *parent,
		"cloneDb":      *cloneDB,
		"dryRun":       *dryRun,
		"phpVersion":   *php,
		"dbEngine":     *dbEngine,
		"dbVersion":    *dbVersion,
		"redisVersion": *redis,
		"worktreeRoot": *worktreeRoot,
	}
	var resp sites.CreateWorktreeResult
	if err := cli.Call(ctx, "site.create_worktree", params, &resp); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}

	if *jsonOut {
		if err := printJSON(env.Stdout, resp); err != nil {
			return ExitError
		}
		return ExitOK
	}

	if *dryRun {
		_, _ = fmt.Fprint(env.Stdout, resp.DryRunPreview)
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stdout, "Created %s (slug=%s)\n", resp.Site.Name, resp.DerivedSlug)
	_, _ = fmt.Fprintf(env.Stdout, "URL: https://%s\n", resp.Site.Domain)
	if resp.Site.WorktreePath != "" {
		_, _ = fmt.Fprintf(env.Stdout, "Worktree: %s\n", resp.Site.WorktreePath)
	}
	return ExitOK
}

// runSiteDelete dispatches `locorum site delete`.
func runSiteDelete(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("site delete", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	purge := fs.Bool("purge-volume", false, "also remove the database volume (destroys data)")
	skipSnap := fs.Bool("skip-snapshot", false, "skip the auto-snapshot taken before deletion")
	force := fs.Bool("force", false, "discard worktree changes without confirmation")
	dryRun := fs.Bool("dry-run", false, "describe the plan without executing")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: locorum site delete <slug-or-id> [--force] [--purge-volume] [--dry-run]")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer func() { _ = cli.Close() }()

	params := siteIDParams(target, map[string]any{
		"purgeVolume":   *purge,
		"skipSnapshot":  *skipSnap,
		"forceWorktree": *force,
		"dryRun":        *dryRun,
	})
	var resp map[string]any
	if err := cli.Call(ctx, "site.delete", params, &resp); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	if *dryRun {
		_, _ = fmt.Fprintln(env.Stdout, "Dry run complete — no changes were made (see daemon log for the full plan).")
	} else {
		_, _ = fmt.Fprintf(env.Stdout, "%s: deleted\n", target)
	}
	return ExitOK
}
