package health

import (
	"context"
	"testing"

	tlspkg "github.com/PeterBooker/locorum/internal/tls"
	"github.com/PeterBooker/locorum/internal/tls/fake"
)

// TestMkcertCheckMissingFiresWarn covers the existing "no mkcert / no
// CA" branch. The Note must be Warn (not Info) because the user can't
// get trusted HTTPS at all without acting on it.
func TestMkcertCheckMissingFiresWarn(t *testing.T) {
	prov := fake.New()
	prov.AvailableStatus = tlspkg.Status{Installed: false, CATrusted: false, Message: "mkcert not found"}

	c := NewMkcertCheck(prov, nil)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out))
	}
	if out[0].Severity != SeverityWarn {
		t.Errorf("severity = %v, want SeverityWarn", out[0].Severity)
	}
	if out[0].ID != "mkcert-missing" {
		t.Errorf("ID = %q, want mkcert-missing", out[0].ID)
	}
}

// TestMkcertCheckCapabilitiesAllClear covers the steady state: mkcert
// installed, CA trusted, no extra trust-store gaps. No findings expected.
func TestMkcertCheckCapabilitiesAllClear(t *testing.T) {
	prov := fake.New()
	// fake.New() default already has Installed+CATrusted true and
	// CapabilitiesValue zero (no Java, no Firefox-on-Windows).

	c := NewMkcertCheck(prov, nil)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected zero findings, got %d: %+v", len(out), out)
	}
}

// TestMkcertCheckSurfaceJavaCapability locks in F19's load-bearing
// behaviour: when mkcert is OK *and* Java is detected on the host, the
// check emits a single Info finding pointing at the cacerts gap. No
// blocker / no warn — this is informational so the user knows Spring /
// Tomcat / SBT need the manual step but the rest of the toolchain is
// fine.
func TestMkcertCheckSurfaceJavaCapability(t *testing.T) {
	prov := fake.New()
	prov.CapabilitiesValue = tlspkg.Capabilities{JavaPresent: true}

	c := NewMkcertCheck(prov, nil)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(out), out)
	}
	if out[0].ID != "mkcert-java-trust" {
		t.Errorf("ID = %q, want mkcert-java-trust", out[0].ID)
	}
	if out[0].Severity != SeverityInfo {
		t.Errorf("severity = %v, want SeverityInfo", out[0].Severity)
	}
	if out[0].Remediation == "" {
		t.Errorf("Remediation should be set")
	}
}

// TestMkcertCheckSurfaceFirefoxCapability covers the Firefox-on-Windows
// half of the capability surface. Same Info-level + remediation copy.
func TestMkcertCheckSurfaceFirefoxCapability(t *testing.T) {
	prov := fake.New()
	prov.CapabilitiesValue = tlspkg.Capabilities{FirefoxOnWindows: true}

	c := NewMkcertCheck(prov, nil)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(out))
	}
	if out[0].ID != "mkcert-firefox-windows" {
		t.Errorf("ID = %q, want mkcert-firefox-windows", out[0].ID)
	}
	if out[0].Severity != SeverityInfo {
		t.Errorf("severity = %v, want SeverityInfo", out[0].Severity)
	}
}

// TestMkcertCheckSurfaceBothCapabilities covers a host that has both
// Java and Firefox-on-Windows; we should see one finding per gap, both
// Info-level, no double-counting.
func TestMkcertCheckSurfaceBothCapabilities(t *testing.T) {
	prov := fake.New()
	prov.CapabilitiesValue = tlspkg.Capabilities{
		JavaPresent:      true,
		FirefoxOnWindows: true,
	}

	c := NewMkcertCheck(prov, nil)
	out, err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 findings, got %d: %+v", len(out), out)
	}
	for _, f := range out {
		if f.Severity != SeverityInfo {
			t.Errorf("finding %q severity = %v, want SeverityInfo", f.ID, f.Severity)
		}
	}
}

// TestMkcertCheckMissingDoesNotProbeCapabilities ensures the
// Capabilities probe is short-circuited when mkcert is missing — the
// Java / Firefox notes only make sense once the base CA is trusted.
// We assert this by giving the fake a Java-present capability AND a
// missing status; the output must still be the single mkcert-missing
// Warn (no Info notes).
func TestMkcertCheckMissingDoesNotProbeCapabilities(t *testing.T) {
	prov := fake.New()
	prov.AvailableStatus = tlspkg.Status{Installed: false}
	prov.CapabilitiesValue = tlspkg.Capabilities{JavaPresent: true, FirefoxOnWindows: true}

	c := NewMkcertCheck(prov, nil)
	out, _ := c.Run(context.Background())
	if len(out) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(out), out)
	}
	if out[0].ID != "mkcert-missing" {
		t.Errorf("ID = %q, want mkcert-missing only when not installed", out[0].ID)
	}
}
