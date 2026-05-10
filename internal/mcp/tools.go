package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/PeterBooker/locorum/internal/daemon"
)

// toolDef pairs a wire descriptor with its implementation. requireFull
// gates the tool to the full profile; readonly tools omit it.
type toolDef struct {
	descriptor  toolDescriptor
	impl        func(ctx context.Context, s *Server, args json.RawMessage) (any, error)
	requireFull bool
}

// findTool looks up a tool by name, honouring the profile gate. Returns
// (toolDef, true) when the tool is reachable in the current profile.
func (s *Server) findTool(name string) (toolDef, bool) {
	for _, t := range allTools {
		if t.descriptor.Name != name {
			continue
		}
		if s.profile == daemon.ProfileReadOnly && t.requireFull {
			return toolDef{}, false
		}
		return t, true
	}
	return toolDef{}, false
}

// toolList returns the descriptors visible in the current profile. The
// readonly profile sees a strict subset.
func (s *Server) toolList() []toolDescriptor {
	out := make([]toolDescriptor, 0, len(allTools))
	for _, t := range allTools {
		if s.profile == daemon.ProfileReadOnly && t.requireFull {
			continue
		}
		out = append(out, t.descriptor)
	}
	return out
}

// schemaSiteRef is the JSON Schema for tools that target one site.
// "siteId" or "slug" — whichever the agent has handy. The daemon
// rejects calls missing both.
const schemaSiteRef = `{
  "type": "object",
  "properties": {
    "siteId": {"type": "string", "description": "Canonical UUID of the site"},
    "slug":   {"type": "string", "description": "Human-friendly slug (e.g. \"my-shop\")"}
  },
  "anyOf": [
    {"required": ["siteId"]},
    {"required": ["slug"]}
  ]
}`

// allTools is the registered tool catalogue. Order is presentation-
// stable: list_sites first, then describe_site, then mutating actions.
var allTools = []toolDef{
	{
		descriptor: toolDescriptor{
			Name:        "list_sites",
			Title:       "List sites",
			Description: "Return every Locorum site with name, slug, status, URL, and runtime versions. Cheap and side-effect-free.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		impl: callListSites,
	},
	{
		descriptor: toolDescriptor{
			Name:        "describe_site",
			Title:       "Describe a site",
			Description: "Return the full SiteDescription for one site (containers, hooks, snapshots count, recent activity). When the MCP server has a scope set, the scoped site is used by default if no siteId or slug is given.",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl: callDescribeSite,
	},
	{
		descriptor: toolDescriptor{
			Name:        "recent_activity",
			Title:       "Recent activity",
			Description: "Return the most recent lifecycle events for a site (start / stop / clone / snapshot etc.). Read-only.",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl: callRecentActivity,
	},
	{
		descriptor: toolDescriptor{
			Name:  "read_log",
			Title: "Read container log",
			Description: "Return the trailing N lines of one of a site's service container logs. Service must be one of web/php/database/redis. " +
				"Use this to debug a failing site without granting RCE-equivalent exec access.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId":  {"type": "string"},
    "slug":    {"type": "string"},
    "service": {"type": "string", "enum": ["web", "php", "database", "redis"]},
    "lines":   {"type": "integer", "minimum": 1, "maximum": 5000, "default": 200}
  },
  "required": ["service"]
}`),
		},
		impl: callReadLog,
	},
	{
		descriptor: toolDescriptor{
			Name:        "list_snapshots",
			Title:       "List snapshots",
			Description: "Return the disk-backed database snapshots for a site, newest first. Each entry has filename, size, and engine/version stamps.",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl: callListSnapshots,
	},
	{
		descriptor: toolDescriptor{
			Name:        "list_hooks",
			Title:       "List hooks",
			Description: "Return the configured lifecycle hooks for a site. Read-only; running a hook needs run_hook (full profile).",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl: callListHooks,
	},

	// ─── Mutating tools (full profile) ─────────────────────────────────
	{
		descriptor: toolDescriptor{
			Name:        "start_site",
			Title:       "Start a site",
			Description: "Bring a site's containers up. Idempotent — calling on a running site is a no-op.",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl:        callStartSite,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:        "stop_site",
			Title:       "Stop a site",
			Description: "Stop a site's containers (volumes preserved). Idempotent.",
			InputSchema: json.RawMessage(schemaSiteRef),
		},
		impl:        callStopSite,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:  "wp_cli",
			Title: "Run wp-cli",
			Description: "Execute a wp-cli command inside the site's PHP container. Args are passed verbatim (no shell). " +
				"Example: {\"slug\": \"shop\", \"args\": [\"option\", \"get\", \"siteurl\"]}.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId": {"type": "string"},
    "slug":   {"type": "string"},
    "args":   {"type": "array", "items": {"type": "string"}, "minItems": 1}
  },
  "required": ["args"]
}`),
		},
		impl:        callWPCLI,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:  "create_snapshot",
			Title: "Create a database snapshot",
			Description: "Take a compressed database snapshot of the site. Returns the absolute path on the host. " +
				"Use this before any risky mutation as a recovery point.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId": {"type": "string"},
    "slug":   {"type": "string"},
    "label":  {"type": "string", "maxLength": 32, "default": "manual"}
  }
}`),
		},
		impl:        callCreateSnapshot,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:        "restore_snapshot",
			Title:       "Restore a database snapshot",
			Description: "Replace the site's database with the contents of a snapshot file. Pass force=true to override engine/version checks.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId": {"type": "string"},
    "slug":   {"type": "string"},
    "path":   {"type": "string"},
    "force":  {"type": "boolean", "default": false}
  },
  "required": ["path"]
}`),
		},
		impl:        callRestoreSnapshot,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:        "run_hook",
			Title:       "Run a hook",
			Description: "Run a single configured hook outside the lifecycle. Requires the hookId from list_hooks.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId": {"type": "string"},
    "slug":   {"type": "string"},
    "hookId": {"type": "integer"}
  },
  "required": ["hookId"]
}`),
		},
		// SECURITY: run_hook only references existing hooks by ID — it does
		// NOT accept arbitrary command text. wp-cli/exec/exec-host hooks are
		// shell-evaluated (`sh -c` with token expansion) inside the
		// container, so allowing creation/edit of hooks via MCP would be
		// RCE-equivalent for any agent holding the bearer token. Hook
		// authoring stays GUI-only by design. If an mcp/create_hook tool is
		// ever added it MUST gate behind an interactive confirmation in
		// the full profile and surface the command text to the user before
		// execution. See SECURITY.md M7.
		impl:        callRunHook,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:  "worktree_create",
			Title: "Create a worktree-bound site",
			Description: "Spin up a new Locorum site bound to a git worktree. The site's files dir is the worktree itself, " +
				"so editing files in your IDE updates the running site immediately. Returns the derived slug + URL. " +
				"Pass dryRun=true to preview the plan without committing to it.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "name":         {"type": "string", "description": "human-readable site name"},
    "gitRemote":    {"type": "string", "description": "upstream git URL (ssh or https)"},
    "branch":       {"type": "string", "description": "branch to track"},
    "parentSlug":   {"type": "string", "description": "existing parent site to attach to (else auto-derived from gitRemote)"},
    "cloneDb":      {"type": "boolean", "description": "copy parent DB + run wp search-replace; default false"},
    "phpVersion":   {"type": "string"},
    "dbEngine":     {"type": "string"},
    "dbVersion":    {"type": "string"},
    "redisVersion": {"type": "string"},
    "worktreeRoot": {"type": "string", "description": "directory holding the worktree; default <parent>.worktrees/"},
    "dryRun":       {"type": "boolean", "default": false}
  },
  "required": ["name", "gitRemote", "branch"]
}`),
		},
		impl:        callWorktreeCreate,
		requireFull: true,
	},
	{
		descriptor: toolDescriptor{
			Name:        "worktree_destroy",
			Title:       "Destroy a site (worktree-aware)",
			Description: "Delete a Locorum site, including the git worktree if it is one. Pass force=true to discard uncommitted changes.",
			InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "siteId":       {"type": "string"},
    "slug":         {"type": "string"},
    "force":        {"type": "boolean", "default": false},
    "purgeVolume":  {"type": "boolean", "default": false},
    "skipSnapshot": {"type": "boolean", "default": false},
    "dryRun":       {"type": "boolean", "default": false}
  }
}`),
		},
		impl:        callWorktreeDestroy,
		requireFull: true,
	},
}

// ─── Tool implementations ────────────────────────────────────────────

func callListSites(ctx context.Context, s *Server, _ json.RawMessage) (any, error) {
	var out any
	if err := s.callDaemon(ctx, "site.list", map[string]any{}, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callDescribeSite(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	params["includeActivity"] = true
	params["activityLimit"] = 20
	params["includeSnapshots"] = true
	params["includeHostPort"] = true
	var out any
	if err := s.callDaemon(ctx, "site.describe", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callRecentActivity(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	var out any
	if err := s.callDaemon(ctx, "site.recentActivity", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callReadLog(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID  string `json:"siteId"`
		Slug    string `json:"slug"`
		Service string `json:"service"`
		Lines   int    `json:"lines"`
	}
	var parsed p
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	params["service"] = parsed.Service
	if parsed.Lines > 0 {
		params["lines"] = parsed.Lines
	}
	var out any
	if err := s.callDaemon(ctx, "site.logs", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callListSnapshots(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	var out any
	if err := s.callDaemon(ctx, "snapshot.list", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callListHooks(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	var out any
	if err := s.callDaemon(ctx, "hook.list", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callStartSite(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	var out any
	if err := s.callDaemon(ctx, "site.start", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callStopSite(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	params, err := siteRefArgs(s, args)
	if err != nil {
		return nil, err
	}
	var out any
	if err := s.callDaemon(ctx, "site.stop", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callWPCLI(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID string   `json:"siteId"`
		Slug   string   `json:"slug"`
		Args   []string `json:"args"`
	}
	var parsed p
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if len(parsed.Args) == 0 {
		return nil, errors.New("args is required and must be a non-empty list of wp-cli arguments")
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	params["args"] = parsed.Args
	var out any
	if err := s.callDaemon(ctx, "site.wp", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callCreateSnapshot(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID string `json:"siteId"`
		Slug   string `json:"slug"`
		Label  string `json:"label"`
	}
	var parsed p
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	if parsed.Label != "" {
		params["label"] = parsed.Label
	}
	var out any
	if err := s.callDaemon(ctx, "snapshot.create", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callRestoreSnapshot(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID string `json:"siteId"`
		Slug   string `json:"slug"`
		Path   string `json:"path"`
		Force  bool   `json:"force"`
	}
	var parsed p
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if parsed.Path == "" {
		return nil, errors.New("path is required")
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	params["path"] = parsed.Path
	params["force"] = parsed.Force
	var out any
	if err := s.callDaemon(ctx, "snapshot.restore", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callWorktreeCreate(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	// Pass through verbatim — the daemon validates and applies its own
	// shape rules. Forward the raw arguments as the params object.
	var raw map[string]any
	if len(args) > 0 {
		if err := json.Unmarshal(args, &raw); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	} else {
		raw = map[string]any{}
	}
	var out any
	if err := s.callDaemon(ctx, "site.create_worktree", raw, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callWorktreeDestroy(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID       string `json:"siteId"`
		Slug         string `json:"slug"`
		Force        bool   `json:"force"`
		PurgeVolume  bool   `json:"purgeVolume"`
		SkipSnapshot bool   `json:"skipSnapshot"`
		DryRun       bool   `json:"dryRun"`
	}
	var parsed p
	if len(args) > 0 {
		if err := json.Unmarshal(args, &parsed); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	params["forceWorktree"] = parsed.Force
	params["purgeVolume"] = parsed.PurgeVolume
	params["skipSnapshot"] = parsed.SkipSnapshot
	params["dryRun"] = parsed.DryRun
	var out any
	if err := s.callDaemon(ctx, "site.delete", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

func callRunHook(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	type p struct {
		SiteID string `json:"siteId"`
		Slug   string `json:"slug"`
		HookID int64  `json:"hookId"`
	}
	var parsed p
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if parsed.HookID == 0 {
		return nil, errors.New("hookId is required")
	}
	params := siteRefMap(s, parsed.SiteID, parsed.Slug)
	params["hookId"] = parsed.HookID
	var out any
	if err := s.callDaemon(ctx, "hook.run", params, &out); err != nil {
		return nil, mapDaemonErr(err)
	}
	return out, nil
}

// ─── helpers ────────────────────────────────────────────────────────

// siteRefArgs decodes a tools/call argument blob and returns the
// site-ref params map ready to forward to the daemon. Honours the
// MCP scope: when no explicit siteId/slug is set, fall back to the
// scope (which we know is one of the two forms — daemon will accept
// it as a slug if it matches).
func siteRefArgs(s *Server, args json.RawMessage) (map[string]any, error) {
	type ref struct {
		SiteID string `json:"siteId"`
		Slug   string `json:"slug"`
	}
	var parsed ref
	if len(args) > 0 {
		if err := json.Unmarshal(args, &parsed); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
	}
	return siteRefMap(s, parsed.SiteID, parsed.Slug), nil
}

// siteRefMap builds a {siteId, slug} map, falling back to s.scope when
// neither was provided. Empty maps are still valid (the daemon will
// reject them with a clear "params must include siteId or slug" error).
func siteRefMap(s *Server, siteID, slug string) map[string]any {
	out := map[string]any{}
	if siteID == "" && slug == "" && s.scope != "" {
		// We don't know whether the scope is a UUID or slug; pass it
		// as slug — the daemon's resolver tries both.
		out["slug"] = s.scope
		return out
	}
	if siteID != "" {
		out["siteId"] = siteID
	}
	if slug != "" {
		out["slug"] = slug
	}
	return out
}

// mapDaemonErr re-shapes daemon RPC errors so the MCP agent sees a
// clean message without the "rpc error: code=… message=…" framing.
func mapDaemonErr(err error) error {
	if err == nil {
		return nil
	}
	var rpc *daemon.RPCError
	if errors.As(err, &rpc) {
		return errors.New(rpc.Message)
	}
	return err
}
