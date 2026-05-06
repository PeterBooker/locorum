package cli

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/hooks"
)

// runHook dispatches `locorum hook …`. Limited to non-destructive
// operations for v1 (list, run); CRUD is GUI-driven and lives in the
// hooks panel. CLI hooks management is a P5 follow-on.
func runHook(ctx context.Context, env *Env) ExitCode {
	if len(env.Args) == 0 {
		fmt.Fprintln(env.Stderr, "usage: locorum hook <list|run> [args...]")
		return ExitUsage
	}
	verb := env.Args[0]
	rest := *env
	rest.Args = env.Args[1:]
	switch verb {
	case "list", "ls":
		return runHookList(ctx, &rest)
	case "run":
		return runHookRunCmd(ctx, &rest)
	case "help", "-h", "--help":
		fmt.Fprintln(env.Stdout, "hook list <slug>             List hooks attached to a site")
		fmt.Fprintln(env.Stdout, "hook run <slug> --id <hook>  Run a single hook outside the lifecycle")
		return ExitOK
	default:
		fmt.Fprintf(env.Stderr, "locorum hook: unknown verb %q\n", verb)
		return ExitUsage
	}
}

func runHookList(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("hook list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum hook list <slug>")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	var resp struct {
		Hooks []hooks.Hook `json:"hooks"`
	}
	if err := cli.Call(ctx, "hook.list", siteIDParams(target, nil), &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	if *jsonOut {
		if err := printJSON(env.Stdout, resp.Hooks); err != nil {
			return ExitError
		}
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tEVENT\tTYPE\tENABLED\tCOMMAND")
	for _, h := range resp.Hooks {
		enabled := "off"
		if h.Enabled {
			enabled = "on"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n",
			h.ID, h.Event, h.TaskType, enabled, truncate(h.Command, 60))
	}
	if err := tw.Flush(); err != nil {
		return ExitError
	}
	if len(resp.Hooks) == 0 {
		fmt.Fprintln(env.Stdout, "(no hooks)")
	}
	return ExitOK
}

func runHookRunCmd(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("hook run", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	hookID := fs.Int64("id", 0, "hook id (from `hook list`)")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 || *hookID == 0 {
		fmt.Fprintln(env.Stderr, "usage: locorum hook run <slug> --id <hook-id>")
		return ExitUsage
	}
	target := fs.Arg(0)

	cli, err := dial(ctx, env, daemon.HelloOptions{})
	if err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	defer cli.Close()

	params := siteIDParams(target, map[string]any{"hookId": *hookID})
	var resp struct {
		Result hooks.Result `json:"result"`
	}
	if err := cli.Call(ctx, "hook.run", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	r := resp.Result
	fmt.Fprintf(env.Stdout, "exit=%d duration=%s lines=%d\n", r.ExitCode, r.Duration(), r.LinesEmitted)
	if r.LogPath != "" {
		fmt.Fprintf(env.Stdout, "log: %s\n", r.LogPath)
	}
	if r.Err != nil {
		fmt.Fprintln(env.Stderr, "hook error:", r.Err)
	}
	if r.ExitCode != 0 || r.Err != nil {
		return ExitError
	}
	return ExitOK
}

// truncate caps s at n runes, appending an ellipsis when shortened.
// Used for the table view so a multi-line `wp` command doesn't make
// the output unreadable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
