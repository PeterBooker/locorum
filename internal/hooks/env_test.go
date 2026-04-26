package hooks

import (
	"runtime"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// asMap converts a "KEY=VALUE" slice into a map for assertion ergonomics.
func asMap(t *testing.T, vars []string) map[string]string {
	t.Helper()
	m := make(map[string]string, len(vars))
	for _, kv := range vars {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			t.Fatalf("malformed env entry %q (missing '=')", kv)
		}
		k, v := kv[:idx], kv[idx+1:]
		if _, dup := m[k]; dup {
			t.Fatalf("duplicate env key %q", k)
		}
		m[k] = v
	}
	return m
}

func TestBuildEnv_NilSite(t *testing.T) {
	if got := BuildEnv(nil, ContextContainer); got != nil {
		t.Fatalf("expected nil for nil site, got %v", got)
	}
}

func TestBuildEnv_FullSite_Container(t *testing.T) {
	site := &types.Site{
		ID:           "site-uuid-1",
		Name:         "Demo",
		Slug:         "demo",
		Domain:       "demo.localhost",
		FilesDir:     "/home/u/locorum/sites/demo",
		PublicDir:    "wp-content/public",
		PHPVersion:   "8.3",
		MySQLVersion: "8.0",
		RedisVersion: "7.4",
		DBPassword:   "topsecret",
		WebServer:    "apache",
		Multisite:    "subdomain",
	}

	got := asMap(t, BuildEnv(site, ContextContainer))

	want := map[string]string{
		"LOCORUM_VERSION":       version.Version,
		"LOCORUM_SITE_ID":       "site-uuid-1",
		"LOCORUM_SITE_NAME":     "Demo",
		"LOCORUM_SITE_SLUG":     "demo",
		"LOCORUM_DOMAIN":        "demo.localhost",
		"LOCORUM_PRIMARY_URL":   "https://demo.localhost",
		"LOCORUM_PHP_VERSION":   "8.3",
		"LOCORUM_MYSQL_VERSION": "8.0",
		"LOCORUM_REDIS_VERSION": "7.4",
		"LOCORUM_WEBSERVER":     "apache",
		"LOCORUM_MULTISITE":     "subdomain",
		"LOCORUM_FILES_DIR":     "/home/u/locorum/sites/demo",
		"LOCORUM_PUBLIC_DIR":    "wp-content/public",
		"LOCORUM_DB_HOST":       "database",
		"LOCORUM_DB_NAME":       "wordpress",
		"LOCORUM_DB_USER":       "wordpress",
		"LOCORUM_DB_PASSWORD":   "topsecret",
		"LOCORUM_DB_PORT":       "3306",
		"LOCORUM_OS":            runtime.GOOS,
		"LOCORUM_GUI":           "1",
		"LOCORUM_CONTEXT":       "container",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("variable count = %d, want %d (extras: %v)", len(got), len(want), diffKeys(got, want))
	}
}

func TestBuildEnv_HostContext(t *testing.T) {
	site := &types.Site{
		ID:         "id",
		Slug:       "demo",
		Domain:     "demo.localhost",
		DBPassword: "pw",
		WebServer:  "nginx",
	}

	got := asMap(t, BuildEnv(site, ContextHost))

	if got["LOCORUM_DB_HOST"] != "127.0.0.1" {
		t.Errorf("LOCORUM_DB_HOST = %q, want 127.0.0.1", got["LOCORUM_DB_HOST"])
	}
	if got["LOCORUM_DB_PORT"] != HostDBPort {
		t.Errorf("LOCORUM_DB_PORT = %q, want %q", got["LOCORUM_DB_PORT"], HostDBPort)
	}
	if got["LOCORUM_CONTEXT"] != "host" {
		t.Errorf("LOCORUM_CONTEXT = %q, want host", got["LOCORUM_CONTEXT"])
	}
}

func TestBuildEnv_DomainFallback(t *testing.T) {
	site := &types.Site{Slug: "fallback"}
	got := asMap(t, BuildEnv(site, ContextContainer))

	if got["LOCORUM_DOMAIN"] != "fallback.localhost" {
		t.Errorf("LOCORUM_DOMAIN = %q, want fallback.localhost", got["LOCORUM_DOMAIN"])
	}
	if got["LOCORUM_PRIMARY_URL"] != "https://fallback.localhost" {
		t.Errorf("LOCORUM_PRIMARY_URL = %q", got["LOCORUM_PRIMARY_URL"])
	}
}

func TestBuildEnv_WebServerDefault(t *testing.T) {
	site := &types.Site{Slug: "x"}
	got := asMap(t, BuildEnv(site, ContextContainer))
	if got["LOCORUM_WEBSERVER"] != "nginx" {
		t.Errorf("LOCORUM_WEBSERVER default = %q, want nginx", got["LOCORUM_WEBSERVER"])
	}
}

func TestBuildEnv_EmptyMultisitePassesThrough(t *testing.T) {
	site := &types.Site{Slug: "s", Multisite: ""}
	got := asMap(t, BuildEnv(site, ContextContainer))
	if v, ok := got["LOCORUM_MULTISITE"]; !ok || v != "" {
		t.Errorf("LOCORUM_MULTISITE = %q, ok=%v; want empty string and present", v, ok)
	}
}

func TestBuildEnv_NoDomainNoSlug(t *testing.T) {
	site := &types.Site{ID: "id"}
	got := asMap(t, BuildEnv(site, ContextContainer))
	if got["LOCORUM_DOMAIN"] != "" {
		t.Errorf("LOCORUM_DOMAIN = %q, want empty", got["LOCORUM_DOMAIN"])
	}
	if got["LOCORUM_PRIMARY_URL"] != "" {
		t.Errorf("LOCORUM_PRIMARY_URL = %q, want empty", got["LOCORUM_PRIMARY_URL"])
	}
}

func TestBuildEnv_StableOrdering(t *testing.T) {
	site := &types.Site{Slug: "s", DBPassword: "p"}
	first := BuildEnv(site, ContextContainer)
	second := BuildEnv(site, ContextContainer)
	if len(first) != len(second) {
		t.Fatalf("len mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("ordering differs at idx %d: %q vs %q", i, first[i], second[i])
		}
	}
}

func TestBuildEnv_OnlyOneEqualsPerEntry(t *testing.T) {
	site := &types.Site{Slug: "s", Name: "Has=Equals", DBPassword: "p=q"}
	for _, entry := range BuildEnv(site, ContextContainer) {
		idx := strings.IndexByte(entry, '=')
		if idx < 0 {
			t.Errorf("missing '=' in %q", entry)
		}
		// Values may legitimately contain '=' characters (passwords); just
		// confirm we have at least one separator for the parser.
	}
}

func TestBuildEnv_GUIVariableTogglesOff(t *testing.T) {
	old := runningUnderGUI
	runningUnderGUI = func() bool { return false }
	defer func() { runningUnderGUI = old }()

	got := asMap(t, BuildEnv(&types.Site{Slug: "s"}, ContextContainer))
	if got["LOCORUM_GUI"] != "0" {
		t.Errorf("LOCORUM_GUI = %q, want 0", got["LOCORUM_GUI"])
	}
}

func TestEnvContext_String(t *testing.T) {
	if ContextContainer.String() != "container" {
		t.Errorf("ContextContainer = %q", ContextContainer.String())
	}
	if ContextHost.String() != "host" {
		t.Errorf("ContextHost = %q", ContextHost.String())
	}
}

func diffKeys(got, want map[string]string) []string {
	var extras []string
	for k := range got {
		if _, ok := want[k]; !ok {
			extras = append(extras, k)
		}
	}
	return extras
}
