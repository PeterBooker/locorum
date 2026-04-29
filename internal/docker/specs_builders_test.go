package docker

import (
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
	if out != original {
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
