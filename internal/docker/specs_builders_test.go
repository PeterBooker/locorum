package docker

import (
	"errors"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func builderTestSite() *types.Site {
	return &types.Site{
		ID:           "id1",
		Slug:         "demo",
		Domain:       "demo.localhost",
		FilesDir:     "/tmp/demo",
		PHPVersion:   "8.2",
		DBEngine:     "mysql",
		DBVersion:    "8.0",
		MySQLVersion: "8.0", // legacy mirror retained for one minor
		RedisVersion: "7",
		WebServer:    "nginx",
		DBPassword:   "supersecret",
	}
}

// TestBuilders_AllProduceHardenedDefaults asserts every spec builder
// applies the engine-grade security baseline. PHP is the documented
// exception — wodby/php's entrypoint uses `sudo`, which is incompatible
// with NoNewPrivileges=true. The exception is asserted separately below
// so a regression that silently re-enables hardening for PHP (which would
// break startup) fails this test, and a regression that silently weakens
// some other role would also fail.
func TestBuilders_AllProduceHardenedDefaults(t *testing.T) {
	site := builderTestSite()
	specs := []ContainerSpec{
		NginxWebSpec(site, "/home/x"),
		ApacheWebSpec(site, "/home/x"),
		// DatabaseSpec moved to internal/dbengine/{mysql,mariadb}.go;
		// hardened-defaults coverage there.
		RedisSpec(site),
		MailSpec(),
		AdminerSpec(),
	}
	for _, s := range specs {
		t.Run(s.Name, func(t *testing.T) {
			if !contains(s.Security.CapDrop, "ALL") {
				t.Errorf("CapDrop missing ALL: %v", s.Security.CapDrop)
			}
			if !s.Security.NoNewPrivileges {
				t.Errorf("NoNewPrivileges = false, want true")
			}
			if !s.Init {
				t.Errorf("Init = false, want true")
			}
			if s.Restart != RestartNo {
				t.Errorf("Restart = %q, want %q", s.Restart, RestartNo)
			}
			if s.Resources.LogMaxSize == "" {
				t.Errorf("LogMaxSize empty (default not applied)")
			}
			if s.Resources.LogMaxFiles == 0 {
				t.Errorf("LogMaxFiles = 0 (default not applied)")
			}
		})
	}
}

// TestPHPSpec_PermissiveSecurity locks in the documented exception:
// wodby/php's entrypoint uses sudo, so we cannot run it under
// NoNewPrivileges. The empty CapDrop opts back into Docker's default cap
// set (sudo additionally needs AUDIT_WRITE and SETPCAP).
func TestPHPSpec_PermissiveSecurity(t *testing.T) {
	site := builderTestSite()
	spec := PHPSpec(site, "/home/x")
	if spec.Security.NoNewPrivileges {
		t.Errorf("NoNewPrivileges = true; wodby/php uses sudo and needs it disabled")
	}
	if contains(spec.Security.CapDrop, "ALL") {
		t.Errorf("CapDrop includes ALL; wodby/php needs Docker's default cap set")
	}
	if !spec.Init {
		t.Errorf("Init = false, want true")
	}
}

func TestPHPSpec_PasswordOnlyInEnvSecrets(t *testing.T) {
	site := builderTestSite()
	spec := PHPSpec(site, "/home/x")

	for _, e := range spec.Env {
		if strings.Contains(e, site.DBPassword) {
			t.Errorf("plaintext password in Env entry: %q", e)
		}
	}
	found := false
	for _, sec := range spec.EnvSecrets {
		if sec.Key == "MYSQL_PASSWORD" && sec.Value == site.DBPassword {
			found = true
		}
	}
	if !found {
		t.Errorf("MYSQL_PASSWORD not in EnvSecrets")
	}
}

// DatabaseSpec password / healthcheck coverage now lives in
// internal/dbengine/mysql_test.go and mariadb_test.go.

// TestPHPSpec_SPXOff asserts the unconditional SPX wiring (the
// /etc/.../zzz-spx.ini bind mount) is present even when SPX is
// disabled, so toggling SPX on later only mutates env + the data-dir
// bind — not the INI mount that should stay stable across toggles.
// It also asserts none of the SPX-conditional state leaks in.
func TestPHPSpec_SPXOff(t *testing.T) {
	site := builderTestSite()
	site.SPXEnabled = false
	site.SPXKey = "should-not-leak"

	spec := PHPSpec(site, "/home/x")

	if !hasBindTarget(spec.Mounts, "/usr/local/etc/php/conf.d/zzz-spx.ini") {
		t.Errorf("zzz-spx.ini bind mount missing while SPX disabled (toggle would churn the hash)")
	}
	if hasBindTarget(spec.Mounts, "/var/spx/data") {
		t.Errorf("/var/spx/data should not be mounted while SPX disabled")
	}
	if hasBindTarget(spec.Mounts, "/usr/local/etc/php/conf.d/zzzz-spx-key.ini") {
		t.Errorf("per-site SPX key INI mount present while SPX disabled")
	}
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "PHP_EXTENSIONS_ENABLE=") || strings.HasPrefix(e, "PHP_EXTENSIONS_DISABLE=") {
			t.Errorf("PHP_EXTENSIONS_* env present while SPX disabled: %q", e)
		}
	}
}

// TestPHPSpec_SPXOn asserts enabling SPX adds the data-dir bind, the
// env-driven activation knobs, and the redacted key; that the key is
// never on the plaintext Env slice; and that toggling SPX changes the
// config hash so EnsureContainer correctly recreates the container.
func TestPHPSpec_SPXOn(t *testing.T) {
	site := builderTestSite()
	site.SPXEnabled = true
	site.SPXKey = "secret-spx-key"

	off := PHPSpec(builderTestSite(), "/home/x")
	on := PHPSpec(site, "/home/x")

	if off.ConfigHash() == on.ConfigHash() {
		t.Errorf("toggling SPX did not change ConfigHash; container would not be recreated")
	}
	if !hasBindTarget(on.Mounts, "/var/spx/data") {
		t.Errorf("/var/spx/data bind missing when SPX enabled")
	}
	if !hasBindTarget(on.Mounts, "/usr/local/etc/php/conf.d/zzzz-spx-key.ini") {
		t.Errorf("per-site SPX key INI bind missing when SPX enabled")
	}

	// The key INI bind source must point at the canonical per-site
	// path so EnsureSPXStep and PHPSpec stay in lockstep.
	wantSrc := SPXKeyINIPath("/home/x", site.Slug)
	foundSrc := false
	for _, m := range on.Mounts {
		if m.Bind != nil && m.Bind.Target == "/usr/local/etc/php/conf.d/zzzz-spx-key.ini" {
			if m.Bind.Source != wantSrc {
				t.Errorf("key INI bind source = %q, want %q", m.Bind.Source, wantSrc)
			}
			if !m.Bind.ReadOnly {
				t.Errorf("key INI bind is not read-only — PHP-FPM should never mutate it")
			}
			foundSrc = true
		}
	}
	if !foundSrc {
		t.Errorf("key INI bind not found in mounts")
	}

	wantEnabled := false
	wantDisabled := false
	for _, e := range on.Env {
		if e == "PHP_EXTENSIONS_ENABLE=spx" {
			wantEnabled = true
		}
		if e == "PHP_EXTENSIONS_DISABLE=xdebug,xhprof" {
			wantDisabled = true
		}
		if strings.Contains(e, site.SPXKey) {
			t.Errorf("SPX key value leaked into plaintext Env entry: %q", e)
		}
	}
	if !wantEnabled {
		t.Errorf("PHP_EXTENSIONS_ENABLE=spx missing on Env: %v", on.Env)
	}
	if !wantDisabled {
		t.Errorf("PHP_EXTENSIONS_DISABLE=xdebug,xhprof missing on Env: %v", on.Env)
	}

	// SPX key must NOT travel as an env var any more — the env-var
	// pivot was unreliable through wodby/php's pool config and the
	// per-site INI bind mount is now the source of truth.
	for _, sec := range on.EnvSecrets {
		if sec.Key == "SPX_KEY" {
			t.Errorf("SPX_KEY found in EnvSecrets; key should travel via the per-site INI mount only")
		}
	}
}

// hasBindTarget reports whether mounts contains a BindMount targeting
// the given container path.
func hasBindTarget(mounts []Mount, target string) bool {
	for _, m := range mounts {
		if m.Bind != nil && m.Bind.Target == target {
			return true
		}
	}
	return false
}

func TestSpec_HealthcheckRequired(t *testing.T) {
	site := builderTestSite()
	for _, spec := range []ContainerSpec{
		NginxWebSpec(site, "/home/x"),
		PHPSpec(site, "/home/x"),
		RedisSpec(site),
		MailSpec(),
		AdminerSpec(),
	} {
		if spec.Healthcheck == nil || len(spec.Healthcheck.Test) == 0 {
			t.Errorf("%s: missing healthcheck", spec.Name)
		}
	}
}

func TestPHPSpec_UID_GID_Pair(t *testing.T) {
	site := builderTestSite()
	spec := PHPSpec(site, "/home/x")
	if spec.User == "" {
		t.Errorf("PHP spec User empty")
	}
	if !strings.Contains(spec.User, ":") {
		t.Errorf("PHP spec User = %q, want uid:gid form", spec.User)
	}
}

func TestRedactErrSpec_DoesNotDoubleRedact(t *testing.T) {
	spec := ContainerSpec{
		Name:       "x",
		Image:      "alpine:3",
		EnvSecrets: []EnvSecret{{Key: "K", Value: "abc"}},
	}
	original := errSimple("create container x: ok")
	out := redactErrSpec(original, spec)
	// Nothing to redact — should return the original error.
	if !errors.Is(out, original) {
		t.Errorf("redactErrSpec returned a new error when nothing was redacted: %v", out)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
