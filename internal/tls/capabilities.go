package tls

import (
	"context"
	"os"
	"os/exec"
)

// Capabilities reports which downstream trust stores the user's host
// environment cares about. mkcert installs into the OS root store and
// (on Linux/macOS) the per-user NSS store automatically; everything
// else needs the user to run `mkcert -install` from a context where the
// relevant tooling is in PATH. We can't do that from a GUI process
// (launchctl strips JAVA_HOME on macOS, and we lack the shell context
// in general), so the contract is: detect, surface, let the user act.
//
// Each field answers "should the user care about this trust store on
// this machine?". When true *and* mkcert is installed, the user's next
// step is documented in the matching health Note.
type Capabilities struct {
	// JavaPresent is true when a Java toolchain is detectable on the
	// host (JAVA_HOME set, or `java` on PATH). Spring Boot / Tomcat /
	// SBT users in this bucket need to re-run `mkcert -install` from a
	// terminal where JAVA_HOME resolves so the cacerts keystore picks
	// up the local CA.
	JavaPresent bool

	// FirefoxOnWindows is true when the host is Windows AND a Firefox
	// install is detectable. Firefox on Windows uses its own NSS DB
	// that mkcert can only populate after Firefox has been launched at
	// least once (NSS lazy-creates the profile dir on first run). The
	// remediation copy in the matching Note tells the user to launch
	// Firefox once and re-run the install.
	FirefoxOnWindows bool
}

// detectJavaPresent is the cross-platform half of [Capabilities]. The
// signal is the union of two cheap probes — JAVA_HOME being set, or
// `java` resolving on PATH. Either alone is enough; both being false
// means mkcert's Java keystore branch wouldn't have anything to write
// to, so the user doesn't need a Note.
func detectJavaPresent() bool {
	if v := os.Getenv("JAVA_HOME"); v != "" {
		return true
	}
	if _, err := exec.LookPath("java"); err == nil {
		return true
	}
	return false
}

// Capabilities reports the host's trust-store environment for the
// caller. Pure best-effort — every probe failure short-circuits to
// "not detected" and the caller treats a zero-value Capabilities as
// "no extra notes needed".
//
// Cheap: JAVA_HOME is an env-var read; `java` PATH lookup is a stat;
// the Firefox-on-Windows leg short-circuits on non-Windows hosts
// without doing any I/O.
func (m *Mkcert) Capabilities(_ context.Context) Capabilities {
	return Capabilities{
		JavaPresent:      detectJavaPresent(),
		FirefoxOnWindows: detectFirefoxOnWindows(),
	}
}
