package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/PeterBooker/locorum/internal/daemon"
	"github.com/PeterBooker/locorum/internal/mcp"
)

// runMCP dispatches `locorum mcp …`. Supports stdio (default; what
// Claude Code, Cursor, Continue use locally) and HTTP (loopback-only
// with bearer auth, for remote / multi-agent setups).
func runMCP(ctx context.Context, env *Env) ExitCode {
	if len(env.Args) == 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: locorum mcp <serve|rotate-token> [flags]")
		return ExitUsage
	}
	verb := env.Args[0]
	rest := *env
	rest.Args = env.Args[1:]
	switch verb {
	case "serve":
		return runMCPServe(ctx, &rest)
	case "rotate-token":
		return runMCPRotateToken(&rest)
	case "help", "-h", "--help":
		_, _ = fmt.Fprintln(env.Stdout, "mcp serve --stdio [--profile full|readonly]")
		_, _ = fmt.Fprintln(env.Stdout, "    Run an MCP server on stdin/stdout. Reads LOCORUM_MCP_SCOPE")
		_, _ = fmt.Fprintln(env.Stdout, "    from env to scope every tool call to a single site (defence-in-depth).")
		_, _ = fmt.Fprintln(env.Stdout, "mcp serve --http 127.0.0.1:2484 [--profile full|readonly]")
		_, _ = fmt.Fprintln(env.Stdout, "    Serve MCP over HTTP. Loopback bind only; bearer-token auth")
		_, _ = fmt.Fprintln(env.Stdout, "    using the secret in ~/.locorum/state/mcp_token.")
		_, _ = fmt.Fprintln(env.Stdout, "mcp rotate-token")
		_, _ = fmt.Fprintln(env.Stdout, "    Regenerate the HTTP MCP bearer token.")
		return ExitOK
	default:
		_, _ = fmt.Fprintf(env.Stderr, "locorum mcp: unknown verb %q\n", verb)
		return ExitUsage
	}
}

// runMCPRotateToken regenerates the bearer token used by HTTP MCP and
// prints the new value. Existing in-flight HTTP MCP servers continue
// to use the old value until restarted; the user copies the new token
// into their MCP client config and runs `locorum mcp serve --http`
// again.
func runMCPRotateToken(env *Env) ExitCode {
	tok, err := mcp.RotateToken(env.HomeDir)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum:", err)
		return ExitError
	}
	_, _ = fmt.Fprintln(env.Stdout, tok)
	return ExitOK
}

func runMCPServe(ctx context.Context, env *Env) ExitCode {
	fs := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	stdio := fs.Bool("stdio", false, "use stdin/stdout (default when no --http set)")
	httpBind := fs.String("http", "", "bind HTTP MCP server (e.g. 127.0.0.1:2484)")
	profile := fs.String("profile", "full", "trust tier: full | readonly")
	if err := fs.Parse(env.Args); err != nil {
		return ExitUsage
	}
	if !*stdio && *httpBind == "" {
		// Default to stdio when neither flag is set.
		*stdio = true
	}
	if *stdio && *httpBind != "" {
		_, _ = fmt.Fprintln(env.Stderr, "locorum mcp: --stdio and --http are mutually exclusive")
		return ExitUsage
	}
	switch *profile {
	case daemon.ProfileFull, daemon.ProfileReadOnly:
	default:
		_, _ = fmt.Fprintf(env.Stderr, "locorum mcp: unknown profile %q\n", *profile)
		return ExitUsage
	}

	scope := os.Getenv("LOCORUM_MCP_SCOPE")

	// Open the IPC client up front. We pass the profile + scope in
	// the hello so the daemon enforces both — this server is a thin
	// shim, the daemon is the security boundary.
	cli, err := dial(ctx, env, daemon.HelloOptions{
		PeerKind: "mcp",
		Profile:  *profile,
		MCPScope: scope,
	})
	if err != nil {
		// Diagnostics go to stderr — stdout is reserved for MCP frames
		// and any extra bytes there confuse the connected agent.
		_, _ = fmt.Fprintln(env.Stderr, "locorum mcp:", err)
		return errToExit(err)
	}
	defer func() { _ = cli.Close() }()

	// MCP logs go to stderr too, never stdout. Replace the default
	// logger for the lifetime of this command so any package-level
	// slog.Info call doesn't poison the protocol stream.
	logger := slog.New(slog.NewJSONHandler(env.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	srv := mcp.NewServer(mcp.Options{
		In:      os.Stdin,
		Out:     os.Stdout,
		Client:  cli,
		Logger:  logger,
		Scope:   scope,
		Profile: *profile,
		Version: env.Version,
	})

	if *stdio {
		if err := srv.Serve(ctx); err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "locorum mcp:", err)
			return ExitError
		}
		return ExitOK
	}

	// HTTP transport. Bring up a loopback listener with bearer auth.
	token, err := mcp.LoadOrCreateToken(env.HomeDir)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum mcp:", err)
		return ExitError
	}
	httpSrv, err := mcp.NewHTTPServer(mcp.HTTPOptions{
		Bind:   *httpBind,
		Token:  token,
		Server: srv,
		Logger: logger,
	})
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum mcp:", err)
		return ExitUsage
	}
	_, _ = fmt.Fprintf(env.Stderr, "locorum mcp http listening on %s\n", *httpBind)
	_, _ = fmt.Fprintf(env.Stderr, "auth token: %s (also at %s)\n", token, mcp.TokenPath(env.HomeDir))
	if err := httpSrv.Serve(ctx); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "locorum mcp:", err)
		return ExitError
	}
	return ExitOK
}
