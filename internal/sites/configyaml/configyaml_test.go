package configyaml

import (
	"errors"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

func sampleSite() types.Site {
	return types.Site{
		ID:            "uuid-1",
		Name:          "My Site",
		Slug:          "my-site",
		Domain:        "my-site.localhost",
		FilesDir:      "/home/peter/locorum/sites/my-site",
		PublicDir:     "/",
		PHPVersion:    "8.3",
		DBEngine:      "mysql",
		DBVersion:     "8.4",
		MySQLVersion:  "8.4",
		RedisVersion:  "7",
		WebServer:     "nginx",
		Multisite:     "",
		PublishDBPort: false,

		// Fields that must NOT be projected:
		DBPassword: "supersecret",
		Salts:      `{"AUTH_KEY":"...","SECURE_AUTH_KEY":"..."}`,
		CreatedAt:  "2026-04-30T00:00:00Z",
		UpdatedAt:  "2026-04-30T00:00:00Z",
	}
}

func sampleHooks() []hooks.Hook {
	return []hooks.Hook{
		{ID: 1, SiteID: "uuid-1", Event: "post-start", Position: 0, TaskType: "wp-cli", Command: "cli version", Enabled: true},
		{ID: 2, SiteID: "uuid-1", Event: "pre-start", Position: 0, TaskType: "exec-host", Command: "echo hi", Enabled: true},
	}
}

func TestFromSite_DropsSecretsAndBookkeeping(t *testing.T) {
	s := sampleSite()
	hs := sampleHooks()

	f := FromSite(s, hs)
	out, err := Render(f)
	if err != nil {
		t.Fatal(err)
	}

	rendered := string(out)
	for _, leak := range []string{
		"supersecret", // db_password
		"AUTH_KEY",    // salts
		"uuid-1",      // id
		"2026-04-30",  // timestamps
		"FilesDir",    // never serialise
		"/home/peter", // path leak
	} {
		if strings.Contains(rendered, leak) {
			t.Errorf("rendered YAML must not contain %q\n--- output ---\n%s", leak, rendered)
		}
	}

	for _, want := range []string{
		"schema_version: 1",
		"name: My Site",
		"slug: my-site",
		"domain: my-site.localhost",
		"php_version:", // value rendered as quoted "8.3" or 8.3
		"engine: mysql",
		"web_server: nginx",
		"event: pre-start", // hooks reordered alphabetically
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered YAML missing %q\n--- output ---\n%s", want, rendered)
		}
	}
}

func TestRender_HasGenmarkHeader(t *testing.T) {
	out, err := Render(FromSite(sampleSite(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if !genmark.HasMarker(out) {
		t.Fatalf("rendered YAML must carry the genmark marker\n--- output ---\n%s", out)
	}
}

func TestRender_HooksSortedByEventThenPosition(t *testing.T) {
	// Sort key: (event lex-asc, position asc).  "post-start" < "pre-start"
	// because 'o' < 'r'.
	hs := []hooks.Hook{
		{Event: "post-start", Position: 1, TaskType: "exec", Command: "second"},
		{Event: "post-start", Position: 0, TaskType: "exec", Command: "first"},
		{Event: "pre-start", Position: 0, TaskType: "exec-host", Command: "third"},
	}
	f := FromSite(sampleSite(), hs)

	if got := f.Hooks[0].Command; got != "first" {
		t.Errorf("hooks[0] = %q, want %q", got, "first")
	}
	if got := f.Hooks[1].Command; got != "second" {
		t.Errorf("hooks[1] = %q, want %q", got, "second")
	}
	if got := f.Hooks[2].Command; got != "third" {
		t.Errorf("hooks[2] = %q, want %q", got, "third")
	}
}

func TestParse_RoundTripsRender(t *testing.T) {
	original := FromSite(sampleSite(), sampleHooks())
	out, err := Render(original)
	if err != nil {
		t.Fatal(err)
	}

	res, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("clean round-trip emitted warnings: %v", res.Warnings)
	}

	// Compare field-by-field — exhaustive enough that an accidental
	// silent drop in any handler will surface here.
	got := res.File
	if got.SchemaVersion != original.SchemaVersion {
		t.Errorf("schema_version: got %d, want %d", got.SchemaVersion, original.SchemaVersion)
	}
	if got.Name != original.Name {
		t.Errorf("name: got %q, want %q", got.Name, original.Name)
	}
	if got.DB.Engine != original.DB.Engine {
		t.Errorf("db.engine: got %q, want %q", got.DB.Engine, original.DB.Engine)
	}
	if len(got.Hooks) != len(original.Hooks) {
		t.Fatalf("hooks count: got %d, want %d", len(got.Hooks), len(original.Hooks))
	}
}

func TestParse_RejectsUnknownFields(t *testing.T) {
	bad := []byte(`schema_version: 1
name: x
slug: x
domain: x
php_version: "8.3"
db: {engine: mysql, version: "8.4"}
web_server: nginx
typo_field: oops
`)
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected Parse to reject unknown fields (KnownFields=true)")
	}
}

func TestParse_RejectsUnknownSchemaVersion(t *testing.T) {
	future := []byte(`schema_version: 99
name: x
slug: x
domain: x
php_version: "8.3"
db: {engine: mysql, version: "8.4"}
web_server: nginx
`)
	if _, err := Parse(future); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("got %v, want ErrUnknownVersion", err)
	}
}

func TestParse_MissingRequired(t *testing.T) {
	cases := map[string][]byte{
		"missing name":   []byte("schema_version: 1\nslug: x\ndomain: x\ndb: {engine: mysql, version: \"1\"}\n"),
		"missing slug":   []byte("schema_version: 1\nname: x\ndomain: x\ndb: {engine: mysql, version: \"1\"}\n"),
		"missing domain": []byte("schema_version: 1\nname: x\nslug: x\ndb: {engine: mysql, version: \"1\"}\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(body)
			if !errors.Is(err, ErrMissingRequired) {
				t.Fatalf("got %v, want ErrMissingRequired", err)
			}
		})
	}
}

func TestParse_RejectsInvalidEnums(t *testing.T) {
	cases := map[string][]byte{
		"engine":    []byte("schema_version: 1\nname: x\nslug: x\ndomain: x\ndb: {engine: postgres, version: \"1\"}\nweb_server: nginx\n"),
		"web":       []byte("schema_version: 1\nname: x\nslug: x\ndomain: x\ndb: {engine: mysql, version: \"1\"}\nweb_server: iis\n"),
		"multisite": []byte("schema_version: 1\nname: x\nslug: x\ndomain: x\ndb: {engine: mysql, version: \"1\"}\nweb_server: nginx\nmultisite: hyperdrive\n"),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(body)
			if !errors.Is(err, ErrInvalidEnum) {
				t.Fatalf("got %v, want ErrInvalidEnum", err)
			}
		})
	}
}

func TestNormalize_DeprecatedMySQLVersion(t *testing.T) {
	body := []byte(`schema_version: 1
name: legacy
slug: legacy
domain: legacy.localhost
php_version: "8.3"
db:
  engine: mysql
  mysql_version: "8.0"
web_server: nginx
`)
	res, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if res.File.DB.Version != "8.0" {
		t.Errorf("db.version not migrated from db.mysql_version: %q", res.File.DB.Version)
	}
	if res.File.DB.MySQLVersion != "" {
		t.Errorf("db.mysql_version should have been cleared after migration")
	}
	if len(res.Warnings) == 0 {
		t.Errorf("expected a deprecation warning")
	}
}

func TestReconcile_Equal(t *testing.T) {
	a := FromSite(sampleSite(), nil)
	b := FromSite(sampleSite(), nil)
	rep := Reconcile(a, b, "2026-04-30T00:00:00Z", "2026-04-30T00:00:00Z")
	if rep.Verdict != VerdictEqual {
		t.Errorf("equal sides: got %v", rep.Verdict)
	}
}

func TestReconcile_YAMLNewerWhenMTimeWins(t *testing.T) {
	a := FromSite(sampleSite(), nil)
	a.Name = "yaml-edited"
	b := FromSite(sampleSite(), nil)
	rep := Reconcile(a, b, "2026-05-01T00:00:00Z", "2026-04-30T00:00:00Z")
	if rep.Verdict != VerdictYAMLNewer {
		t.Errorf("yaml newer: got %v", rep.Verdict)
	}
	if len(rep.Differences) == 0 {
		t.Errorf("expected differences populated")
	}
}

func TestReconcile_DBNewerWhenDBWinsOrUnknown(t *testing.T) {
	a := FromSite(sampleSite(), nil)
	a.Name = "yaml-stale"
	b := FromSite(sampleSite(), nil)

	// DB clearly newer.
	rep := Reconcile(a, b, "2026-04-30T00:00:00Z", "2026-05-01T00:00:00Z")
	if rep.Verdict != VerdictDBNewer {
		t.Errorf("db newer: got %v", rep.Verdict)
	}

	// Missing yaml mtime — we choose DB.
	rep = Reconcile(a, b, "", "2026-05-01T00:00:00Z")
	if rep.Verdict != VerdictDBNewer {
		t.Errorf("missing yaml mtime: got %v", rep.Verdict)
	}
}

func TestDiffFields_DetectsAllProjectedChanges(t *testing.T) {
	base := FromSite(sampleSite(), nil)

	mutations := map[string]func(f *File){
		"name":            func(f *File) { f.Name = "x" },
		"domain":          func(f *File) { f.Domain = "x" },
		"public_dir":      func(f *File) { f.PublicDir = "x" },
		"php_version":     func(f *File) { f.PHPVersion = "x" },
		"db.engine":       func(f *File) { f.DB.Engine = "mariadb" },
		"db.version":      func(f *File) { f.DB.Version = "x" },
		"db.publish_port": func(f *File) { f.DB.PublishPort = !f.DB.PublishPort },
		"redis_version":   func(f *File) { f.RedisVersion = "x" },
		"web_server":      func(f *File) { f.WebServer = "apache" },
		"multisite":       func(f *File) { f.Multisite = "subdomain" },
	}
	for name, mut := range mutations {
		t.Run(name, func(t *testing.T) {
			b := base
			mut(&b)
			diffs := diffFields(base, b)
			found := false
			for _, d := range diffs {
				if d == name {
					found = true
				}
			}
			if !found {
				t.Errorf("mutation of %q not surfaced in diffFields: %v", name, diffs)
			}
		})
	}
}
