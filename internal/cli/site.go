package cli

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/sites"
)

// runSite is the dispatcher for `locorum site …`. Each verb has its own
// flag set so adding one doesn't require touching the others.
func runSite(ctx context.Context, env *Env) ExitCode {
	if len(env.Args) == 0 {
		fmt.Fprintln(env.Stderr, "usage: locorum site <list|describe|start|stop|wp|logs> [args...]")
		return ExitUsage
	}
	verb := env.Args[0]
	rest := *env
	rest.Args = env.Args[1:]
	switch verb {
	case "list", "ls":
		return runSiteList(ctx, &rest)
	case "describe", "show":
		return runSiteDescribe(ctx, &rest)
	case "start":
		return runSiteToggle(ctx, &rest, "site.start", "started")
	case "stop":
		return runSiteToggle(ctx, &rest, "site.stop", "stopped")
	case "create":
		return runSiteCreate(ctx, &rest)
	case "delete", "rm":
		return runSiteDelete(ctx, &rest)
	case "wp":
		return runSiteWP(ctx, &rest)
	case "logs":
		return runSiteLogs(ctx, &rest)
	case "help", "-h", "--help":
		fmt.Fprintln(env.Stdout, "site list                                List sites")
		fmt.Fprintln(env.Stdout, "site describe <slug-or-id>               Print one site's full state")
		fmt.Fprintln(env.Stdout, "site start <slug-or-id>                  Start a site")
		fmt.Fprintln(env.Stdout, "site stop <slug-or-id>                   Stop a site")
		fmt.Fprintln(env.Stdout, "site create --name N --git-remote URL --branch B [--clone-db] [--dry-run]")
		fmt.Fprintln(env.Stdout, "                                         Create a worktree-bound site")
		fmt.Fprintln(env.Stdout, "site delete <slug-or-id> [--force] [--purge-volume] [--dry-run]")
		fmt.Fprintln(env.Stdout, "                                         Delete a site")
		fmt.Fprintln(env.Stdout, "site wp <slug-or-id> -- <args...>        Run a wp-cli command")
		fmt.Fprintln(env.Stdout, "site logs <slug-or-id> --service S       Tail container logs")
		return ExitOK
	default:
		fmt.Fprintf(env.Stderr, "locorum site: unknown verb %q\n", verb)
		return ExitUsage
	}
}

// ─── site list ─────────────────────────────────────────────────────────

func runSiteList(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("site list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON instead of a table")
	includeActivity := fs.Bool("activity", false, "include each site's recent activity")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	var resp []sites.SiteDescription
	params := map[string]any{"includeActivity": *includeActivity}
	if err := cli.Call(ctx, "site.list", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}

	if *jsonOut {
		if err := printJSON(env.Stdout, resp); err != nil {
			return ExitError
		}
		return ExitOK
	}

	// Stable ordering for table output: alphabetical by slug. The
	// daemon already emits in DB-row order, but a list rendered in
	// "whichever order GetSites returns" is harder to read after the
	// row count climbs into the dozens.
	sort.Slice(resp, func(i, j int) bool { return resp[i].Slug < resp[j].Slug })

	tw := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tNAME\tSTATUS\tURL\tPHP\tDB")
	for _, s := range resp {
		status := "stopped"
		if s.Started {
			status = "running"
		}
		db := s.Database.Engine
		if s.Database.Version != "" {
			db = s.Database.Engine + ":" + s.Database.Version
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Slug, s.Name, status, s.URL, s.PHP.Version, db)
	}
	if err := tw.Flush(); err != nil {
		return ExitError
	}
	if len(resp) == 0 {
		fmt.Fprintln(env.Stdout, "(no sites yet — create one in the GUI)")
	}
	return ExitOK
}

// ─── site describe ─────────────────────────────────────────────────────

func runSiteDescribe(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("site describe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON instead of a human-readable summary")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum site describe <slug-or-id>")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	var desc sites.SiteDescription
	params := siteIDParams(target, map[string]any{
		"includeActivity":  true,
		"activityLimit":    20,
		"includeSnapshots": true,
		"includeHostPort":  true,
	})
	if err := cli.Call(ctx, "site.describe", params, &desc); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}

	if *jsonOut {
		if err := printJSON(env.Stdout, desc); err != nil {
			return ExitError
		}
		return ExitOK
	}

	printDescribeText(env.Stdout, desc)
	return ExitOK
}

// printDescribeText renders a multi-line summary suited to a terminal.
// Intentionally flat (no nesting) so a one-screen `site describe` shows
// everything important without scrolling.
func printDescribeText(w fmtWriter, d sites.SiteDescription) {
	fmt.Fprintf(w, "Name:      %s\n", d.Name)
	fmt.Fprintf(w, "Slug:      %s\n", d.Slug)
	fmt.Fprintf(w, "URL:       %s\n", d.URL)
	fmt.Fprintf(w, "Status:    %s\n", statusString(d.Started))
	fmt.Fprintf(w, "Files:     %s\n", d.FilesDir)
	if d.PublicDir != "" && d.PublicDir != "/" {
		fmt.Fprintf(w, "Public:    %s\n", d.PublicDir)
	}
	fmt.Fprintf(w, "WebServer: %s\n", d.WebServer)
	if d.Multisite != "" {
		fmt.Fprintf(w, "Multisite: %s\n", d.Multisite)
	}
	fmt.Fprintf(w, "PHP:       %s\n", d.PHP.Version)
	dbLine := d.Database.Engine + " " + d.Database.Version
	if d.Database.HostPort > 0 {
		dbLine += fmt.Sprintf(" (host port %d)", d.Database.HostPort)
	}
	fmt.Fprintf(w, "Database:  %s\n", dbLine)
	fmt.Fprintf(w, "Redis:     %s\n", d.Redis.Version)
	if d.Hooks.Total > 0 {
		fmt.Fprintf(w, "Hooks:     %d configured\n", d.Hooks.Total)
	}
	if d.SnapshotsCount > 0 {
		fmt.Fprintf(w, "Snapshots: %d\n", d.SnapshotsCount)
	}
	if d.Profiling.Enabled {
		fmt.Fprintln(w, "Profiling: enabled (SPX)")
	}
	if len(d.Activity) > 0 {
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Recent activity:")
		for _, a := range d.Activity {
			fmt.Fprintf(w, "  %s  %-12s  %-10s  %s\n",
				a.Time.Format("2006-01-02 15:04"),
				a.Kind, a.Status, a.Message)
		}
	}
}

// statusString renders the bool as a human word — "running" instead of
// "true" reads better in a terminal.
func statusString(started bool) string {
	if started {
		return "running"
	}
	return "stopped"
}

// ─── site start / stop ─────────────────────────────────────────────────

func runSiteToggle(ctx context.Context, env *Env, method, verb string) ExitCode {
	fs := flag.NewFlagSet("site "+verb, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum site "+verb+" <slug-or-id>")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	var resp map[string]any
	if err := cli.Call(ctx, method, siteIDParams(target, nil), &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	fmt.Fprintf(env.Stdout, "%s: %s\n", target, verb)
	return ExitOK
}

// ─── site wp -- <args...> ──────────────────────────────────────────────

// runSiteWP parses `locorum site wp <slug> -- arg1 arg2 …`. The `--`
// separator is required so flag parsing doesn't fight wp-cli's own
// flags (e.g. `wp option get --orderby=date` — the inner --orderby
// would otherwise be claimed by our flag set).
func runSiteWP(ctx context.Context, env *Env) ExitCode {
	args := env.Args
	if len(args) < 2 {
		fmt.Fprintln(env.Stderr, "usage: locorum site wp <slug> -- <wp-cli args...>")
		return ExitUsage
	}
	target := args[0]
	rest := args[1:]
	if rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(env.Stderr, "locorum: provide at least one wp-cli argument after --")
		return ExitUsage
	}

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	params := siteIDParams(target, map[string]any{"args": rest})
	var resp struct {
		Output string `json:"output"`
	}
	if err := cli.Call(ctx, "site.wp", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	if resp.Output != "" {
		if !strings.HasSuffix(resp.Output, "\n") {
			resp.Output += "\n"
		}
		fmt.Fprint(env.Stdout, resp.Output)
	}
	return ExitOK
}

// ─── site logs ─────────────────────────────────────────────────────────

func runSiteLogs(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("site logs", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	service := fs.String("service", "php", "container service: web | php | database | redis")
	lines := fs.Int("lines", 200, "number of trailing lines to fetch")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum site logs <slug-or-id> [--service S] [--lines N]")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	params := siteIDParams(target, map[string]any{
		"service": *service,
		"lines":   *lines,
	})
	var resp struct {
		Output  string `json:"output"`
		Service string `json:"service"`
	}
	if err := cli.Call(ctx, "site.logs", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	fmt.Fprint(env.Stdout, resp.Output)
	if !strings.HasSuffix(resp.Output, "\n") {
		fmt.Fprintln(env.Stdout)
	}
	return ExitOK
}

// ─── helpers ───────────────────────────────────────────────────────────

// fmtWriter is the io.Writer-superset accepted by Fprintf. Aliased so
// tests can pass a *bytes.Buffer without an extra interface dance.
type fmtWriter interface {
	Write(p []byte) (int, error)
}

// siteIDParams builds the param object every site-scoped method
// expects. extra is merged in non-destructively. A target that looks
// like a UUIDv4 is sent as siteId; everything else as slug.
func siteIDParams(target string, extra map[string]any) map[string]any {
	params := map[string]any{}
	if extra != nil {
		for k, v := range extra {
			params[k] = v
		}
	}
	if looksLikeUUID(target) {
		params["siteId"] = target
	} else {
		params["slug"] = target
	}
	return params
}

// looksLikeUUID is a cheap check — if the target has dashes and the
// canonical 8-4-4-4-12 length, treat it as a UUID. Anything else
// (including slug values that happen to have dashes) goes to slug.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
