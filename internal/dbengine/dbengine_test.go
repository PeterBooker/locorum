package dbengine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/dbengine/fake"
	"github.com/PeterBooker/locorum/internal/types"
)

func TestFor_KnownAndUnknown(t *testing.T) {
	for _, k := range AllKinds() {
		eng, err := For(k)
		if err != nil {
			t.Errorf("For(%q) returned error: %v", k, err)
		}
		if eng.Kind() != k {
			t.Errorf("For(%q).Kind() = %q", k, eng.Kind())
		}
	}
	if _, err := For(Kind("postgres")); err == nil {
		t.Error("For(\"postgres\") should error — Postgres is not supported")
	}
}

func TestIsValid(t *testing.T) {
	if !IsValid(MySQL) {
		t.Error("MySQL should be valid")
	}
	if !IsValid(MariaDB) {
		t.Error("MariaDB should be valid")
	}
	if IsValid(Kind("nosql")) {
		t.Error("nosql should be invalid")
	}
}

func TestResolve_FallsBackToDefault(t *testing.T) {
	site := &types.Site{DBEngine: ""}
	eng := Resolve(site)
	if eng.Kind() != Default {
		t.Errorf("Resolve(empty engine) = %q, want %q", eng.Kind(), Default)
	}

	site = &types.Site{DBEngine: "garbage"}
	eng = Resolve(site)
	if eng.Kind() != Default {
		t.Errorf("Resolve(invalid engine) = %q, want default", eng.Kind())
	}
}

func TestMySQLEngine_ContainerSpecBasics(t *testing.T) {
	site := &types.Site{
		Slug:       "demo",
		DBEngine:   string(MySQL),
		DBVersion:  "8.4",
		DBPassword: "supersecret",
	}
	spec := MustFor(MySQL).ContainerSpec(site, "/home/x")

	if spec.Image != "mysql:8.4" {
		t.Errorf("Image = %q", spec.Image)
	}
	if !contains(spec.Security.CapDrop, "ALL") {
		t.Errorf("CapDrop should include ALL: %v", spec.Security.CapDrop)
	}
	if !spec.Security.NoNewPrivileges {
		t.Error("NoNewPrivileges should be true")
	}
	for _, e := range spec.Env {
		if strings.Contains(e, "supersecret") {
			t.Errorf("plaintext password leaked into Env: %q", e)
		}
	}
	hasPwd := false
	for _, sec := range spec.EnvSecrets {
		if sec.Key == "MYSQL_ROOT_PASSWORD" && sec.Value == "supersecret" {
			hasPwd = true
		}
	}
	if !hasPwd {
		t.Error("MYSQL_ROOT_PASSWORD missing from EnvSecrets")
	}
}

func TestMariaDBEngine_ContainerSpecBasics(t *testing.T) {
	site := &types.Site{
		Slug:       "demo",
		DBEngine:   string(MariaDB),
		DBVersion:  "11.4",
		DBPassword: "topsecret",
	}
	spec := MustFor(MariaDB).ContainerSpec(site, "/home/x")

	if spec.Image != "mariadb:11.4" {
		t.Errorf("Image = %q", spec.Image)
	}
	for _, e := range spec.Env {
		if strings.Contains(e, "topsecret") {
			t.Errorf("plaintext password leaked into Env: %q", e)
		}
	}
}

func TestMySQLFilters_DropCreateAndUse(t *testing.T) {
	in := strings.NewReader(`-- header
CREATE DATABASE prod;
USE prod;
CREATE TABLE wp_users (id INT);
INSERT INTO wp_users VALUES (1);
`)
	var out bytes.Buffer
	if _, err := FilterImportStream(MustFor(MySQL).Filters(), in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, dropped := range []string{"CREATE DATABASE", "USE prod"} {
		if strings.Contains(got, dropped) {
			t.Errorf("expected %q dropped: %q", dropped, got)
		}
	}
	for _, kept := range []string{"-- header", "CREATE TABLE wp_users", "INSERT INTO wp_users"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %q kept: %q", kept, got)
		}
	}
}

func TestMariaDBFilters_StripSandboxComment(t *testing.T) {
	in := strings.NewReader("/*!999999\\- enable the sandbox mode */\nCREATE TABLE wp_users (id INT);\n")
	var out bytes.Buffer
	if _, err := FilterImportStream(MustFor(MariaDB).Filters(), in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "sandbox") {
		t.Errorf("sandbox comment not stripped:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CREATE TABLE wp_users") {
		t.Errorf("table line lost:\n%s", out.String())
	}
}

func TestMariaDBFilters_RewriteUCA1400(t *testing.T) {
	in := strings.NewReader("CREATE TABLE x (n VARCHAR(10) COLLATE utf8mb4_uca1400_ai_ci);\n")
	var out bytes.Buffer
	if _, err := FilterImportStream(MustFor(MariaDB).Filters(), in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "uca1400") {
		t.Errorf("uca1400 not rewritten: %q", out.String())
	}
	if !strings.Contains(out.String(), "utf8mb4_unicode_ci") {
		t.Errorf("expected utf8mb4_unicode_ci replacement: %q", out.String())
	}
}

func TestMySQLEngine_Snapshot_StreamsThroughExecer(t *testing.T) {
	site := &types.Site{Slug: "demo", DBEngine: string(MySQL), DBVersion: "8.4", DBPassword: "x"}
	ex := fake.New()
	ex.StdoutScript = []string{"-- mysqldump body\nINSERT INTO x VALUES (1);\n"}
	ex.ExitScript = []int{0}

	var buf bytes.Buffer
	n, err := MustFor(MySQL).Snapshot(context.Background(), ex, site, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("Snapshot wrote zero bytes")
	}
	if !strings.Contains(buf.String(), "mysqldump") {
		t.Errorf("buffer missing dump body: %q", buf.String())
	}
	if len(ex.Calls) != 1 {
		t.Fatalf("expected 1 exec call, got %d", len(ex.Calls))
	}
	if ex.Calls[0].Container != "locorum-demo-database" {
		t.Errorf("wrong container: %q", ex.Calls[0].Container)
	}
}

func TestMySQLEngine_Restore_PipesIntoExec(t *testing.T) {
	site := &types.Site{Slug: "demo", DBEngine: string(MySQL), DBVersion: "8.4", DBPassword: "x"}
	ex := fake.New()
	ex.ExitScript = []int{0}

	body := strings.NewReader("INSERT INTO x VALUES (1);\n")
	if err := MustFor(MySQL).Restore(context.Background(), ex, site, body); err != nil {
		t.Fatal(err)
	}
	if len(ex.CapturedStdin) != 1 {
		t.Fatalf("expected 1 stdin payload, got %d", len(ex.CapturedStdin))
	}
	if !strings.Contains(string(ex.CapturedStdin[0]), "INSERT INTO x") {
		t.Errorf("stdin missing body: %q", ex.CapturedStdin[0])
	}
}

func TestUpgradeAllowed(t *testing.T) {
	mysqlEng := MustFor(MySQL)
	mariaEng := MustFor(MariaDB)

	// MySQL: same major only.
	if !mysqlEng.UpgradeAllowed("8.0", "8.4") {
		t.Error("8.0 → 8.4 should be allowed")
	}
	if mysqlEng.UpgradeAllowed("8.0", "5.7") {
		t.Error("8.0 → 5.7 should be blocked")
	}

	// MariaDB: forward only.
	if !mariaEng.UpgradeAllowed("10.11", "11.4") {
		t.Error("10.11 → 11.4 should be allowed")
	}
	if mariaEng.UpgradeAllowed("11.4", "10.11") {
		t.Error("11.4 → 10.11 should be blocked")
	}
}

func TestEncodeDecodeMarker_RoundTrip(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	m := NewMarker(MySQL, "8.4", "0.5.0", now)
	body, err := EncodeMarker(m)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := DecodeMarker(body)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Engine != MySQL || parsed.Version != "8.4" || parsed.LocorumVersion != "0.5.0" {
		t.Errorf("roundtrip mismatch: %+v", parsed)
	}
	if !parsed.Created.Equal(now) {
		t.Errorf("created mismatch: %v vs %v", parsed.Created, now)
	}
}

func TestCompareMarker(t *testing.T) {
	mysqlEng := MustFor(MySQL)
	mariaEng := MustFor(MariaDB)

	t.Run("match", func(t *testing.T) {
		have := VolumeMarker{Engine: MySQL, Version: "8.4"}
		want := VolumeMarker{Engine: MySQL, Version: "8.4"}
		if err := CompareMarker(have, want, mysqlEng); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("engine mismatch refuses", func(t *testing.T) {
		have := VolumeMarker{Engine: MySQL, Version: "8.4"}
		want := VolumeMarker{Engine: MariaDB, Version: "11.4"}
		if err := CompareMarker(have, want, mariaEng); err == nil {
			t.Error("expected refusal")
		}
	})
	t.Run("version downgrade refuses", func(t *testing.T) {
		have := VolumeMarker{Engine: MySQL, Version: "8.4"}
		want := VolumeMarker{Engine: MySQL, Version: "5.7"}
		if err := CompareMarker(have, want, mysqlEng); err == nil {
			t.Error("expected refusal")
		}
	})
	t.Run("safe upgrade allowed", func(t *testing.T) {
		have := VolumeMarker{Engine: MySQL, Version: "8.0"}
		want := VolumeMarker{Engine: MySQL, Version: "8.4"}
		if err := CompareMarker(have, want, mysqlEng); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
