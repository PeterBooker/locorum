package tls

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDetectJavaPresentEnvWins exercises the JAVA_HOME branch. We can't
// rely on the test host having (or not having) java on PATH, so we
// scope the assertion to the env-var leg by setting/unsetting
// JAVA_HOME and stripping `java` from PATH for the duration of the
// test.
func TestDetectJavaPresentEnvWins(t *testing.T) {
	t.Setenv("JAVA_HOME", "/somewhere/jdk")
	t.Setenv("PATH", "") // make `which java` fail regardless of host
	if !detectJavaPresent() {
		t.Errorf("JAVA_HOME=/somewhere/jdk should make detectJavaPresent return true")
	}
}

// TestDetectJavaPresentBothMissing covers the negative case. Empty
// JAVA_HOME + a PATH that can't resolve `java` must yield false. We
// point PATH at an empty temp dir so LookPath fails deterministically.
func TestDetectJavaPresentBothMissing(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", empty)
	if detectJavaPresent() {
		t.Errorf("no JAVA_HOME and no `java` on PATH should yield false")
	}
}

// TestDetectJavaPresentPathLookup exercises the LookPath leg by
// dropping a fake `java` executable in a temp dir and pointing PATH at
// it. The probe doesn't invoke the binary; it only stat's it, so the
// fake doesn't need to be functional.
func TestDetectJavaPresentPathLookup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup semantics on Windows differ; the env-var leg covers detection there")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "java")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", dir)
	if !detectJavaPresent() {
		t.Errorf("`java` on PATH should make detectJavaPresent return true")
	}
}

// TestMkcertCapabilitiesIntegratesProbes confirms the Capabilities
// method wires the per-OS probes through. JavaPresent must reflect the
// env-var probe; FirefoxOnWindows must be false on non-Windows hosts
// (the stub returns false there) regardless of the env.
func TestMkcertCapabilitiesIntegratesProbes(t *testing.T) {
	t.Setenv("JAVA_HOME", "/somewhere/jdk")
	t.Setenv("PATH", "")
	m := NewMkcert(t.TempDir(), t.TempDir())
	got := m.Capabilities(context.Background())
	if !got.JavaPresent {
		t.Errorf("expected JavaPresent=true; got %+v", got)
	}
	if runtime.GOOS != "windows" && got.FirefoxOnWindows {
		t.Errorf("FirefoxOnWindows must be false on non-Windows hosts; got %+v", got)
	}
}

// TestDetectFirefoxOnWindowsIsFalseElsewhere locks in the build-tag
// stub: every non-Windows build returns false unconditionally so the
// MkcertCheck never surfaces a Firefox-on-Windows note off-platform.
func TestDetectFirefoxOnWindowsIsFalseElsewhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub-coverage test; the Windows leg has its own probe")
	}
	if detectFirefoxOnWindows() {
		t.Errorf("non-Windows host returned FirefoxOnWindows=true")
	}
}
