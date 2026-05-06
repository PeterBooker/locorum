package cli

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/sites"
)

// runSnapshot dispatches `locorum snapshot …`.
func runSnapshot(ctx context.Context, env *Env) ExitCode {
	if len(env.Args) == 0 {
		fmt.Fprintln(env.Stderr, "usage: locorum snapshot <list|create|restore> [args...]")
		return ExitUsage
	}
	verb := env.Args[0]
	rest := *env
	rest.Args = env.Args[1:]
	switch verb {
	case "list", "ls":
		return runSnapshotList(ctx, &rest)
	case "create":
		return runSnapshotCreate(ctx, &rest)
	case "restore":
		return runSnapshotRestore(ctx, &rest)
	case "help", "-h", "--help":
		fmt.Fprintln(env.Stdout, "snapshot list <slug>           List snapshots for a site")
		fmt.Fprintln(env.Stdout, "snapshot create <slug> [--label L]")
		fmt.Fprintln(env.Stdout, "snapshot restore <slug> --path P [--force]")
		return ExitOK
	default:
		fmt.Fprintf(env.Stderr, "locorum snapshot: unknown verb %q\n", verb)
		return ExitUsage
	}
}

func runSnapshotList(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("snapshot list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum snapshot list <slug>")
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
		Snapshots []sites.SnapshotInfo `json:"snapshots"`
	}
	if err := cli.Call(ctx, "snapshot.list", siteIDParams(target, nil), &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	if *jsonOut {
		if err := printJSON(env.Stdout, resp.Snapshots); err != nil {
			return ExitError
		}
		return ExitOK
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FILENAME\tLABEL\tENGINE\tVERSION\tSIZE\tCREATED")
	for _, s := range resp.Snapshots {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			s.Filename, s.Label, s.Engine, s.Version, s.SizeBytes,
			s.CreatedAt.Format("2006-01-02 15:04"))
	}
	if err := tw.Flush(); err != nil {
		return ExitError
	}
	if len(resp.Snapshots) == 0 {
		fmt.Fprintln(env.Stdout, "(no snapshots yet)")
	}
	return ExitOK
}

func runSnapshotCreate(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("snapshot create", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	label := fs.String("label", "manual", "short label baked into the filename")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(env.Stderr, "usage: locorum snapshot create <slug> [--label L]")
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
		Path string `json:"path"`
	}
	params := siteIDParams(target, map[string]any{"label": *label})
	if err := cli.Call(ctx, "snapshot.create", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	fmt.Fprintln(env.Stdout, resp.Path)
	return ExitOK
}

func runSnapshotRestore(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("snapshot restore", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	path := fs.String("path", "", "absolute path to the snapshot file (required)")
	force := fs.Bool("force", false, "ignore engine/version mismatch")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if fs.NArg() != 1 || *path == "" {
		fmt.Fprintln(env.Stderr, "usage: locorum snapshot restore <slug> --path /abs/path.sql.zst [--force]")
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
		"path":  *path,
		"force": *force,
	})
	var resp struct{}
	if err := cli.Call(ctx, "snapshot.restore", params, &resp); err != nil {
		fmt.Fprintln(env.Stderr, "locorum:", err)
		return errToExit(err)
	}
	fmt.Fprintln(env.Stdout, "snapshot restored")
	return ExitOK
}
