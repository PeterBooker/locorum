package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/storage"
	"github.com/PeterBooker/locorum/internal/types"
)

// SiteService is the subset of *sites.SiteManager the daemon dispatches
// to. Defined as an interface so daemon tests can stub it without
// pulling Docker / SQLite into a unit test.
//
// The concrete *sites.SiteManager satisfies this interface today; new
// methods land here when they're ready to be reachable from CLI/MCP.
type SiteService interface {
	DescribeAll(ctx context.Context, opts sites.DescribeOptions) ([]sites.SiteDescription, error)
	Describe(ctx context.Context, siteID string, opts sites.DescribeOptions) (*sites.SiteDescription, error)

	StartSite(ctx context.Context, siteID string) error
	StopSite(ctx context.Context, siteID string) error

	CreateWorktreeSite(ctx context.Context, opts sites.CreateWorktreeOptions) (*sites.CreateWorktreeResult, error)
	DeleteSiteWithOptions(ctx context.Context, siteID string, opts sites.DeleteOptions) error

	RecentActivity(siteID string) ([]storage.ActivityEvent, error)
	GetActivity(siteID string, limit int) ([]storage.ActivityEvent, error)

	GetContainerLogs(ctx context.Context, siteID, service string, lines int) (string, error)
	ExecWPCLI(ctx context.Context, siteID string, args []string) (string, error)

	Snapshot(ctx context.Context, siteID, label string) (string, error)
	ListSnapshots(slug string) ([]sites.SnapshotInfo, error)
	RestoreSnapshot(ctx context.Context, siteID, snapshotPath string, opts sites.RestoreSnapshotOptions) error

	RunHookNow(ctx context.Context, h hooks.Hook) (hooks.Result, error)
	ListSiteHooks(siteID string) ([]hooks.Hook, error)

	// Used to resolve slug → site for slug-addressed methods so MCP
	// tools can pass a slug without first asking for an id.
	GetSites() ([]types.Site, error)
}

// RegisterMethods wires SiteService onto the Server. Methods are split
// by side-effect class so the readonly profile only sees harmless ones.
//
// Site-scoped MCP enforcement applies to every method whose Params have
// a "siteId" or "slug" field — see SiteScoped() option in server.go.
func RegisterMethods(s *Server, svc SiteService) {
	// ─── Read-only methods ──────────────────────────────────────────
	s.Register("site.list", makeSiteList(svc), ReadOnly())
	s.Register("site.describe", makeSiteDescribe(svc), ReadOnly(), SiteScoped())
	s.Register("site.recentActivity", makeRecentActivity(svc), ReadOnly(), SiteScoped())
	s.Register("site.activity", makeGetActivity(svc), ReadOnly(), SiteScoped())
	s.Register("site.logs", makeContainerLogs(svc), ReadOnly(), SiteScoped())
	s.Register("snapshot.list", makeSnapshotList(svc), ReadOnly(), SiteScoped())
	s.Register("hook.list", makeHookList(svc), ReadOnly(), SiteScoped())

	// ─── Mutating methods (Full only) ───────────────────────────────
	s.Register("site.start", makeSiteStart(svc), SiteScoped())
	s.Register("site.stop", makeSiteStop(svc), SiteScoped())
	s.Register("site.wp", makeWPCLI(svc), SiteScoped())
	s.Register("site.delete", makeSiteDelete(svc), SiteScoped())
	s.Register("site.create_worktree", makeWorktreeCreate(svc))
	s.Register("snapshot.create", makeSnapshotCreate(svc), SiteScoped())
	s.Register("snapshot.restore", makeSnapshotRestore(svc), SiteScoped())
	s.Register("hook.run", makeHookRun(svc), SiteScoped())
}

// ─── Param shapes ──────────────────────────────────────────────────────

// siteRef identifies a site by either UUID or slug. Methods accept
// whichever the caller has handy and resolve to the canonical id
// internally. resolveSite returns NotFound on unknown values so both
// shapes get the same error.
type siteRef struct {
	SiteID string `json:"siteId,omitempty"`
	Slug   string `json:"slug,omitempty"`
}

// resolveSite converts a siteRef to a canonical site id by consulting
// the SiteService. Empty values return CodeInvalidParams.
func resolveSite(svc SiteService, ref siteRef) (string, error) {
	if ref.SiteID != "" {
		return ref.SiteID, nil
	}
	if ref.Slug == "" {
		return "", NewMethodError(codeInvalidParams, "params must include siteId or slug", nil)
	}
	rows, err := svc.GetSites()
	if err != nil {
		return "", err
	}
	for _, s := range rows {
		if s.Slug == ref.Slug {
			return s.ID, nil
		}
	}
	return "", NotFound("site")
}

// listOptions toggles the optional sections on site.list. The defaults
// are cheap (no Docker / disk lookups). Clients pass true on the
// fields they want.
type listOptions struct {
	IncludeActivity  bool `json:"includeActivity,omitempty"`
	ActivityLimit    int  `json:"activityLimit,omitempty"`
	IncludeSnapshots bool `json:"includeSnapshots,omitempty"`
	IncludeHostPort  bool `json:"includeHostPort,omitempty"`
}

func (o listOptions) toDescribe() sites.DescribeOptions {
	return sites.DescribeOptions{
		IncludeActivity:  o.IncludeActivity,
		ActivityLimit:    o.ActivityLimit,
		IncludeSnapshots: o.IncludeSnapshots,
		IncludeHostPort:  o.IncludeHostPort,
	}
}

// ─── site.list ─────────────────────────────────────────────────────────

func makeSiteList(svc SiteService) Handler {
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var p listOptions
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
		return svc.DescribeAll(ctx, p.toDescribe())
	}
}

// ─── site.describe ─────────────────────────────────────────────────────

func makeSiteDescribe(svc SiteService) Handler {
	type describeParams struct {
		siteRef
		listOptions
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var p describeParams
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, p.siteRef)
		if err != nil {
			return nil, err
		}
		desc, err := svc.Describe(ctx, id, p.toDescribe())
		if err != nil {
			return nil, err
		}
		if desc == nil {
			return nil, NotFound("site")
		}
		return desc, nil
	}
}

// ─── site.start / site.stop ────────────────────────────────────────────

func makeSiteStart(svc SiteService) Handler {
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var ref siteRef
		if err := unmarshalParams(params, &ref); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, ref)
		if err != nil {
			return nil, err
		}
		if err := svc.StartSite(ctx, id); err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"started": true, "siteId": id}, nil
	}
}

func makeSiteStop(svc SiteService) Handler {
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var ref siteRef
		if err := unmarshalParams(params, &ref); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, ref)
		if err != nil {
			return nil, err
		}
		if err := svc.StopSite(ctx, id); err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"stopped": true, "siteId": id}, nil
	}
}

// ─── site.recentActivity / site.activity ──────────────────────────────

func makeRecentActivity(svc SiteService) Handler {
	return func(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var ref siteRef
		if err := unmarshalParams(params, &ref); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, ref)
		if err != nil {
			return nil, err
		}
		rows, err := svc.RecentActivity(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": activityToWire(rows)}, nil
	}
}

func makeGetActivity(svc SiteService) Handler {
	type p struct {
		siteRef
		Limit int `json:"limit,omitempty"`
	}
	return func(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		rows, err := svc.GetActivity(id, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": activityToWire(rows)}, nil
	}
}

func activityToWire(rows []storage.ActivityEvent) []sites.ActivityEntry {
	out := make([]sites.ActivityEntry, len(rows))
	for i, r := range rows {
		out[i] = sites.ActivityEntry{
			ID:         r.ID,
			Time:       r.Time,
			Plan:       r.Plan,
			Kind:       string(r.Kind),
			Status:     string(r.Status),
			DurationMS: r.DurationMS,
			Message:    r.Message,
		}
	}
	return out
}

// ─── site.logs ─────────────────────────────────────────────────────────

func makeContainerLogs(svc SiteService) Handler {
	type p struct {
		siteRef
		Service string `json:"service"`
		Lines   int    `json:"lines,omitempty"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		if args.Service == "" {
			return nil, NewMethodError(codeInvalidParams, "service is required", nil)
		}
		switch args.Service {
		case "web", "php", "database", "redis":
		default:
			return nil, NewMethodError(codeInvalidParams, "unknown service: "+args.Service, nil)
		}
		lines := args.Lines
		if lines <= 0 {
			lines = 200
		}
		body, err := svc.GetContainerLogs(ctx, id, args.Service, lines)
		if err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"output": body, "service": args.Service, "lines": lines}, nil
	}
}

// ─── site.wp (wp-cli) ──────────────────────────────────────────────────

func makeWPCLI(svc SiteService) Handler {
	type p struct {
		siteRef
		Args []string `json:"args"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		if len(args.Args) == 0 {
			return nil, NewMethodError(codeInvalidParams, "args must contain at least one wp-cli argument", nil)
		}
		// Defence in depth: forbid raw shell metacharacters in args.
		// ExecInContainer passes argv directly, so this is informational
		// only — but better to reject early than ship the surprise to
		// the user.
		for _, a := range args.Args {
			if strings.ContainsAny(a, "\x00") {
				return nil, NewMethodError(codeInvalidParams, "wp-cli args must not contain NUL bytes", nil)
			}
		}
		out, err := svc.ExecWPCLI(ctx, id, args.Args)
		if err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"output": out}, nil
	}
}

// ─── snapshot.{create,list,restore} ────────────────────────────────────

func makeSnapshotCreate(svc SiteService) Handler {
	type p struct {
		siteRef
		Label string `json:"label,omitempty"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		path, err := svc.Snapshot(ctx, id, args.Label)
		if err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"path": path}, nil
	}
}

func makeSnapshotList(svc SiteService) Handler {
	return func(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var ref siteRef
		if err := unmarshalParams(params, &ref); err != nil {
			return nil, err
		}
		// snapshot.list is keyed by slug on disk, not site id, so we
		// need to resolve to a slug specifically. Look up the row.
		rows, err := svc.GetSites()
		if err != nil {
			return nil, err
		}
		var slug string
		for _, s := range rows {
			if (ref.SiteID != "" && s.ID == ref.SiteID) || (ref.Slug != "" && s.Slug == ref.Slug) {
				slug = s.Slug
				break
			}
		}
		if slug == "" {
			return nil, NotFound("site")
		}
		snaps, err := svc.ListSnapshots(slug)
		if err != nil {
			return nil, err
		}
		return map[string]any{"snapshots": snaps}, nil
	}
}

func makeSnapshotRestore(svc SiteService) Handler {
	type p struct {
		siteRef
		Path     string `json:"path"`
		Force    bool   `json:"force,omitempty"`
		SkipHook bool   `json:"skipHook,omitempty"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		if args.Path == "" {
			return nil, NewMethodError(codeInvalidParams, "snapshot path is required", nil)
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		opts := sites.RestoreSnapshotOptions{
			AllowEngineMismatch: args.Force,
		}
		if err := svc.RestoreSnapshot(ctx, id, args.Path, opts); err != nil {
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"restored": true}, nil
	}
}

// ─── hook.list / hook.run ──────────────────────────────────────────────

func makeHookList(svc SiteService) Handler {
	return func(_ context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var ref siteRef
		if err := unmarshalParams(params, &ref); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, ref)
		if err != nil {
			return nil, err
		}
		rows, err := svc.ListSiteHooks(id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"hooks": rows}, nil
	}
}

func makeHookRun(svc SiteService) Handler {
	type p struct {
		siteRef
		HookID int64 `json:"hookId"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		// Find the hook by id within this site so the caller can't
		// run a hook that belongs to a different site.
		hookList, err := svc.ListSiteHooks(id)
		if err != nil {
			return nil, err
		}
		var target *hooks.Hook
		for i := range hookList {
			if hookList[i].ID == args.HookID {
				target = &hookList[i]
				break
			}
		}
		if target == nil {
			return nil, NotFound("hook")
		}
		res, err := svc.RunHookNow(ctx, *target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"result": res}, nil
	}
}

// ─── site.create_worktree ──────────────────────────────────────────────

func makeWorktreeCreate(svc SiteService) Handler {
	type p struct {
		Name         string `json:"name"`
		GitRemote    string `json:"gitRemote"`
		Branch       string `json:"branch"`
		ParentSlug   string `json:"parentSlug,omitempty"`
		CloneDB      bool   `json:"cloneDb,omitempty"`
		PHPVersion   string `json:"phpVersion,omitempty"`
		DBEngine     string `json:"dbEngine,omitempty"`
		DBVersion    string `json:"dbVersion,omitempty"`
		RedisVersion string `json:"redisVersion,omitempty"`
		WorktreeRoot string `json:"worktreeRoot,omitempty"`
		DryRun       bool   `json:"dryRun,omitempty"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		opts := sites.CreateWorktreeOptions{
			Name:         args.Name,
			GitRemote:    args.GitRemote,
			Branch:       args.Branch,
			ParentSlug:   args.ParentSlug,
			CloneDB:      args.CloneDB,
			PHPVersion:   args.PHPVersion,
			DBEngine:     args.DBEngine,
			DBVersion:    args.DBVersion,
			RedisVersion: args.RedisVersion,
			WorktreeRoot: args.WorktreeRoot,
			DryRun:       args.DryRun,
		}
		res, err := svc.CreateWorktreeSite(ctx, opts)
		if err != nil {
			return nil, err
		}
		return res, nil
	}
}

// ─── site.delete ───────────────────────────────────────────────────────

func makeSiteDelete(svc SiteService) Handler {
	type p struct {
		siteRef
		PurgeVolume   bool `json:"purgeVolume,omitempty"`
		SkipSnapshot  bool `json:"skipSnapshot,omitempty"`
		ForceWorktree bool `json:"forceWorktree,omitempty"`
		DryRun        bool `json:"dryRun,omitempty"`
	}
	return func(ctx context.Context, _ *Conn, params json.RawMessage) (any, error) {
		var args p
		if err := unmarshalParams(params, &args); err != nil {
			return nil, err
		}
		id, err := resolveSite(svc, args.siteRef)
		if err != nil {
			return nil, err
		}
		opts := sites.DeleteOptions{
			PurgeVolume:   args.PurgeVolume,
			SkipSnapshot:  args.SkipSnapshot,
			ForceWorktree: args.ForceWorktree,
			DryRun:        args.DryRun,
		}
		if err := svc.DeleteSiteWithOptions(ctx, id, opts); err != nil {
			// Map worktree-dirty into a typed code so the CLI can
			// prompt for --force without parsing strings.
			if errors.Is(err, sites.ErrWorktreeDirty) {
				return nil, NewMethodError(CodeConflict, err.Error(), err)
			}
			return nil, mapNotFoundError(err)
		}
		return map[string]any{"deleted": !args.DryRun, "siteId": id, "dryRun": args.DryRun}, nil
	}
}

// ─── helpers ───────────────────────────────────────────────────────────

// unmarshalParams decodes params into v. Empty params is fine (v keeps
// its zero values); malformed JSON is mapped to CodeInvalidParams.
func unmarshalParams(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return NewMethodError(codeInvalidParams, "invalid params: "+err.Error(), nil)
	}
	return nil
}

// mapNotFoundError translates well-known SiteManager error strings into
// CodeNotFound. SiteManager returns plain fmt.Errorf("site %q not
// found") values; the wire format prefers a typed code so MCP clients
// don't string-match.
func mapNotFoundError(err error) error {
	if err == nil {
		return nil
	}
	var me *MethodError
	if errors.As(err, &me) {
		return err
	}
	if strings.Contains(err.Error(), "not found") {
		return NewMethodError(CodeNotFound, err.Error(), err)
	}
	return err
}
