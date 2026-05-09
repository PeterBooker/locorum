// Package configyaml projects a site's row + hooks onto a portable
// YAML file at <slug>/.locorum/config.yaml.
//
// SQLite remains Locorum's source of truth; the YAML is a derived
// view, regenerated whenever the site changes. Users who commit
// .locorum/config.yaml to a project repo can re-import the site on a
// fresh machine. The marker line carried in the header lets users
// opt out of regeneration entirely (genmark contract).
//
// What is NOT projected:
//
//   - id, created_at, updated_at — DB-internal bookkeeping; a fresh
//     import generates new values.
//   - db_password — a credential; never on disk outside ~/.locorum/.
//   - salts — also a credential; regenerated per machine if missing.
//   - files_dir — derived from slug + ~/locorum/sites/, so portable
//     across machines without being persisted.
//
// Schema evolution: bump SchemaVersion when an incompatible change
// lands, but always accept the previous N-1 versions. Deprecated
// field names live as struct tags alongside the canonical name and
// move to the canonical name in Normalize, returning a warning
// callers can surface in the System Health panel.
package configyaml

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

// SchemaVersion is the only schema version this build writes. Reading
// an older version is supported through deprecation aliases on
// individual fields; reading a newer version is a hard error
// (forwards-incompatibility is not negotiable — guessing at unknown
// fields would silently lose data).
const SchemaVersion = 1

// Filename is the canonical relative path inside a site directory.
const Filename = ".locorum/config.yaml"

// Allowed enum values. Mirrored here (not imported from the dbengine
// or sites packages) so configyaml stays a leaf and can be parsed
// without booting any other subsystem.
var (
	allowedEngines    = []string{"mysql", "mariadb"}
	allowedWebServers = []string{"nginx", "apache"}
	allowedMultisite  = []string{"", "subdirectory", "subdomain"}
)

// File is the on-disk YAML projection.
//
// Tag conventions:
//   - canonical name in lowercase_underscore
//   - omitempty on optional fields so the rendered file stays short
//   - deprecated names appear in their own field with a `,inline` or
//     dedicated handler (see Normalize).
type File struct {
	SchemaVersion int        `yaml:"schema_version"`
	Name          string     `yaml:"name"`
	Slug          string     `yaml:"slug"`
	Domain        string     `yaml:"domain"`
	PublicDir     string     `yaml:"public_dir"`
	PHPVersion    string     `yaml:"php_version"`
	DB            DBSection  `yaml:"db"`
	RedisVersion  string     `yaml:"redis_version"`
	WebServer     string     `yaml:"web_server"`
	Multisite     string     `yaml:"multisite,omitempty"`
	Hooks         []HookYAML `yaml:"hooks,omitempty"`
}

// DBSection holds the database engine settings. The deprecated
// MySQLVersion alias accepts pre-v1 files that wrote the version
// outside this section.
type DBSection struct {
	Engine      string `yaml:"engine"`
	Version     string `yaml:"version"`
	PublishPort bool   `yaml:"publish_port,omitempty"`

	// MySQLVersion is the pre-v1 alias for Version. Read-only; the
	// renderer never emits it. Normalize moves it into Version when
	// present.
	//
	// Deprecated: use Version + Engine.
	MySQLVersion string `yaml:"mysql_version,omitempty"`
}

// HookYAML projects one site_hooks row. Field names match the
// canonical hook task types: "exec" / "exec-host" / "wp-cli".
type HookYAML struct {
	Event     string `yaml:"event"`
	TaskType  string `yaml:"task_type"`
	Position  int    `yaml:"position"`
	Command   string `yaml:"command"`
	Service   string `yaml:"service,omitempty"`
	RunAsUser string `yaml:"run_as_user,omitempty"`
	Enabled   bool   `yaml:"enabled"`
}

// FromSite projects a Site + its hooks onto a File. Hooks are sorted
// by (event, position) for stable output — yaml.v3 preserves slice
// order on render, so the on-disk layout depends only on input data,
// not on map-iteration randomness.
func FromSite(s types.Site, hs []hooks.Hook) File {
	f := File{
		SchemaVersion: SchemaVersion,
		Name:          s.Name,
		Slug:          s.Slug,
		Domain:        s.Domain,
		PublicDir:     s.PublicDir,
		PHPVersion:    s.PHPVersion,
		DB: DBSection{
			Engine:      s.DBEngine,
			Version:     s.DBVersion,
			PublishPort: s.PublishDBPort,
		},
		RedisVersion: s.RedisVersion,
		WebServer:    s.WebServer,
		Multisite:    s.Multisite,
	}

	// Default to MySQL for legacy rows missing DBEngine — same
	// fallback the rest of the codebase uses (dbengine.Resolve).
	if f.DB.Engine == "" {
		f.DB.Engine = "mysql"
	}
	if f.DB.Version == "" {
		f.DB.Version = s.MySQLVersion //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
	}

	if len(hs) > 0 {
		f.Hooks = make([]HookYAML, 0, len(hs))
		// Sort by (event, position) so the rendered file does not
		// reorder when the storage layer returns rows in a different
		// order across runs.
		sortable := append([]hooks.Hook(nil), hs...)
		sort.Slice(sortable, func(i, j int) bool {
			if sortable[i].Event != sortable[j].Event {
				return sortable[i].Event < sortable[j].Event
			}
			return sortable[i].Position < sortable[j].Position
		})
		for _, h := range sortable {
			f.Hooks = append(f.Hooks, HookYAML{
				Event:     string(h.Event),
				TaskType:  string(h.TaskType),
				Position:  h.Position,
				Command:   h.Command,
				Service:   h.Service,
				RunAsUser: h.RunAsUser,
				Enabled:   h.Enabled,
			})
		}
	}
	return f
}

// Render serialises a File with the canonical genmark header. The
// caller writes the bytes via genmark.WriteIfManaged (or its file-
// owned equivalent).
//
// Render uses 2-space indentation matching the rest of Locorum's
// YAML aesthetics and does not emit a `---` document header — the
// genmark comment block already separates it from the previous
// region of the file (which there isn't one of, but consistency).
func Render(f File) ([]byte, error) {
	if f.SchemaVersion == 0 {
		f.SchemaVersion = SchemaVersion
	}
	var sb strings.Builder
	sb.WriteString(genmark.Header(genmark.StyleHash))

	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("configyaml: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("configyaml: close encoder: %w", err)
	}
	return []byte(sb.String()), nil
}

// Errors. Use errors.Is to match.
var (
	ErrUnknownVersion  = errors.New("configyaml: unknown schema_version")
	ErrInvalidEnum     = errors.New("configyaml: invalid enum value")
	ErrMissingRequired = errors.New("configyaml: missing required field")
)

// ParseResult is the output of Parse — the populated File plus
// non-fatal warnings (e.g. deprecated field used). Warnings are
// strings instead of typed errors so callers can present them
// directly in the GUI.
type ParseResult struct {
	File     File
	Warnings []string
}

// Parse reads YAML bytes into a File, runs Normalize, and validates
// schema version + required fields + enums. The strict-decode mode
// rejects unknown fields so a typo in `php_versoin` does not
// silently default to "" and trash the user's site.
func Parse(data []byte) (ParseResult, error) {
	var f File
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return ParseResult{}, fmt.Errorf("configyaml: decode: %w", err)
	}

	warnings := f.Normalize()

	if f.SchemaVersion == 0 || f.SchemaVersion > SchemaVersion {
		return ParseResult{}, fmt.Errorf("%w: %d (this build supports up to %d)",
			ErrUnknownVersion, f.SchemaVersion, SchemaVersion)
	}
	if f.Name == "" {
		return ParseResult{}, fmt.Errorf("%w: name", ErrMissingRequired)
	}
	if f.Slug == "" {
		return ParseResult{}, fmt.Errorf("%w: slug", ErrMissingRequired)
	}
	if f.Domain == "" {
		return ParseResult{}, fmt.Errorf("%w: domain", ErrMissingRequired)
	}
	if !validEnum(f.DB.Engine, allowedEngines) {
		return ParseResult{}, fmt.Errorf("%w: db.engine=%q (allowed: %s)",
			ErrInvalidEnum, f.DB.Engine, strings.Join(allowedEngines, ", "))
	}
	if f.WebServer != "" && !validEnum(f.WebServer, allowedWebServers) {
		return ParseResult{}, fmt.Errorf("%w: web_server=%q (allowed: %s)",
			ErrInvalidEnum, f.WebServer, strings.Join(allowedWebServers, ", "))
	}
	if !validEnum(f.Multisite, allowedMultisite) {
		return ParseResult{}, fmt.Errorf("%w: multisite=%q (allowed: %s)",
			ErrInvalidEnum, f.Multisite, "subdirectory, subdomain, or empty")
	}

	return ParseResult{File: f, Warnings: warnings}, nil
}

// Normalize migrates any deprecated aliases onto canonical fields and
// returns user-presentable warnings for each migration. Idempotent —
// Normalize on a normalised File is a no-op.
func (f *File) Normalize() []string {
	var warnings []string
	if f.DB.Version == "" && f.DB.MySQLVersion != "" {
		f.DB.Version = f.DB.MySQLVersion
		warnings = append(warnings,
			"db.mysql_version is deprecated; use db.version (will be removed in a future schema)")
	}
	f.DB.MySQLVersion = "" // never write the alias back out
	return warnings
}

func validEnum(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// ReconcileVerdict classifies a single field's drift between the YAML
// file on disk and the DB row.
type ReconcileVerdict int

const (
	// VerdictEqual means YAML and DB agree.
	VerdictEqual ReconcileVerdict = iota
	// VerdictYAMLNewer means the YAML file declares a different
	// value AND the YAML mtime is more recent than the DB row's
	// updated_at. The user probably edited the file and we should
	// offer to apply.
	VerdictYAMLNewer
	// VerdictDBNewer means the DB row is newer; we should regenerate
	// the YAML.
	VerdictDBNewer
)

// ReconcileReport summarises a per-site reconcile pass.
type ReconcileReport struct {
	Verdict     ReconcileVerdict
	Differences []string // human-readable list of changed fields
}

// Reconcile compares an on-disk File to a DB-derived File. The
// caller is responsible for fetching both. Time inputs are RFC3339
// strings (matching storage.Site.UpdatedAt and an os.FileInfo
// mtime); empty strings mean "unknown" and bias the verdict towards
// re-syncing from DB → YAML.
//
// This is a pure function — no IO, no logging — so the GUI can run
// it inside the render path without surprise.
func Reconcile(yamlF, dbF File, yamlMTime, dbUpdatedAt string) ReconcileReport {
	diffs := diffFields(yamlF, dbF)
	if len(diffs) == 0 {
		return ReconcileReport{Verdict: VerdictEqual}
	}
	if yamlMTime == "" || dbUpdatedAt == "" {
		return ReconcileReport{Verdict: VerdictDBNewer, Differences: diffs}
	}
	if yamlMTime > dbUpdatedAt {
		return ReconcileReport{Verdict: VerdictYAMLNewer, Differences: diffs}
	}
	return ReconcileReport{Verdict: VerdictDBNewer, Differences: diffs}
}

// diffFields returns the canonical name of every projected field
// that differs between a and b. Used by Reconcile and exposed for
// tests.
func diffFields(a, b File) []string {
	var diffs []string
	if a.Name != b.Name {
		diffs = append(diffs, "name")
	}
	if a.Domain != b.Domain {
		diffs = append(diffs, "domain")
	}
	if a.PublicDir != b.PublicDir {
		diffs = append(diffs, "public_dir")
	}
	if a.PHPVersion != b.PHPVersion {
		diffs = append(diffs, "php_version")
	}
	if a.DB.Engine != b.DB.Engine {
		diffs = append(diffs, "db.engine")
	}
	if a.DB.Version != b.DB.Version {
		diffs = append(diffs, "db.version")
	}
	if a.DB.PublishPort != b.DB.PublishPort {
		diffs = append(diffs, "db.publish_port")
	}
	if a.RedisVersion != b.RedisVersion {
		diffs = append(diffs, "redis_version")
	}
	if a.WebServer != b.WebServer {
		diffs = append(diffs, "web_server")
	}
	if a.Multisite != b.Multisite {
		diffs = append(diffs, "multisite")
	}
	if !hooksEqual(a.Hooks, b.Hooks) {
		diffs = append(diffs, "hooks")
	}
	return diffs
}

func hooksEqual(a, b []HookYAML) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
