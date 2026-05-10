package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	r := NewRegistry()
	r.Add("supersecret-pw-1234")

	got := r.RedactString("connect failed: pw=supersecret-pw-1234")
	if strings.Contains(got, "supersecret-pw-1234") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, redactionMarker) {
		t.Fatalf("marker not substituted: %q", got)
	}
}

func TestRedactStringNoSecrets(t *testing.T) {
	r := NewRegistry()
	in := "no secrets here"
	if got := r.RedactString(in); got != in {
		t.Fatalf("modified clean string: %q", got)
	}
}

func TestRedactErrPassthrough(t *testing.T) {
	r := NewRegistry()
	r.Add("hidden-token-xxxxxxx")
	err := errors.New("boom: hidden-token-xxxxxxx in argv")
	out := r.Redact(err)
	if out == err { //nolint:errorlint // pointer-identity check: assert Redact returned a *new* instance, not the same value
		t.Fatal("expected new error instance after redaction")
	}
	if strings.Contains(out.Error(), "hidden-token-xxxxxxx") {
		t.Fatalf("token leaked: %q", out.Error())
	}
}

func TestRedactErrNil(t *testing.T) {
	r := NewRegistry()
	if got := r.Redact(nil); got != nil {
		t.Fatalf("nil should round-trip; got %v", got)
	}
}

func TestAddBelowMinIgnored(t *testing.T) {
	r := NewRegistry()
	r.Add("short")
	in := "short message"
	if got := r.RedactString(in); got != in {
		t.Fatalf("short value should not be registered; got %q", got)
	}
}

func TestRemove(t *testing.T) {
	r := NewRegistry()
	r.Add("longsecretvalue")
	r.Remove("longsecretvalue")
	in := "leak: longsecretvalue here"
	if got := r.RedactString(in); got != in {
		t.Fatalf("removed value still redacted; got %q", got)
	}
}

func TestOverlappingSecretsLongestFirst(t *testing.T) {
	r := NewRegistry()
	// Token contains the password as a prefix; redacting the password
	// first would leave a half-token suffix in the output.
	r.Add("password-aaaa-bbbb")
	r.Add("password-aaaa-bbbb-cccc-dddd")

	got := r.RedactString("login: password-aaaa-bbbb-cccc-dddd was used")
	// Either secret may match; what we MUST avoid is leaving any portion
	// of the longer secret unredacted.
	if strings.Contains(got, "cccc") || strings.Contains(got, "dddd") {
		t.Fatalf("longer secret partially leaked: %q", got)
	}
}

func TestConcurrentSafe(t *testing.T) {
	r := NewRegistry()
	r.Add("concurrent-secret-aaaa")
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				_ = r.RedactString("concurrent-secret-aaaa happens")
				r.Add("concurrent-secret-bbbb")
				r.Remove("concurrent-secret-bbbb")
			}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
