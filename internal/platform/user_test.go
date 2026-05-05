package platform

import (
	"regexp"
	"strings"
	"testing"
)

// expectedShape is the post-sanitisation regex; every output string must
// match. Empty strings would fail this regex which is what we want — the
// sanitiser guarantees non-empty output.
var expectedShape = regexp.MustCompile(`^[a-z][a-z0-9._-]*$`)

func TestSanitiseUsernameTable(t *testing.T) {
	cases := []struct {
		in, want, why string
	}{
		// Latin diacritics — the DDEV "André Kraus" bug.
		{"André", "andre", "NFD strip combining marks"},
		{"Müller", "muller", "umlaut decomposes"},
		{"François", "francois", "cedilla decomposes"},
		{"Renée", "renee", "acute decomposes"},

		// NFKC-only cases (fullwidth/compat).
		{"ＡＢＣ", "abc", "fullwidth Latin"},
		{"ﬃ", "ffi", "ligature decomposes via NFKC"},

		// Whitespace and slashes.
		{"Andre Kraus", "andre-kraus", "space → dash"},
		{"DOMAIN\\user", "domain-user", "Windows DOMAIN\\user"},
		{"a/b/c", "a-b-c", "slashes → dash"},
		{"  spaces  ", "spaces", "leading/trailing whitespace trimmed"},

		// Mixed case + special chars.
		{"Foo.Bar_Baz-1", "foo.bar_baz-1", "preserve .,_,-"},
		{"FOO!BAR@BAZ", "foobarbaz", "drop punctuation"},

		// Edge cases that exercise the prefix and trim rules.
		{"123abc", "u-123abc", "leading digit gets u- prefix"},
		{"---x---", "x", "outer dashes trimmed"},
		{"a---b---c", "a-b-c", "internal dash runs collapsed"},
		{".dotfile.", "dotfile", "outer dots trimmed"},

		// Empty/garbage → fallback.
		{"", "locorum", "empty → fallback"},
		{"   ", "locorum", "whitespace-only → fallback"},
		{"!!!@@@###", "locorum", "all punctuation → fallback"},
		{"漢字", "locorum", "non-decomposable CJK → fallback"},

		// Truncation.
		{strings.Repeat("a", 80), strings.Repeat("a", 32), "truncated to 32"},
		// Trailing dash after truncate gets re-trimmed.
		{strings.Repeat("a", 31) + "-extra", "a" + strings.Repeat("a", 30), "truncate doesn't leave trailing dash"},
	}

	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			got := SanitiseUsername(c.in)
			if got != c.want {
				t.Errorf("SanitiseUsername(%q) = %q, want %q (%s)", c.in, got, c.want, c.why)
			}
			if !expectedShape.MatchString(got) {
				t.Errorf("output %q failed expectedShape regex", got)
			}
		})
	}
}

// TestSanitiseUsernameInvariants asserts the contract holds for a small
// hand-picked corpus regardless of the specific output:
//
//   - 1 ≤ len(out) ≤ MaxSanitisedUsernameLen
//   - regex `^[a-z][a-z0-9._-]*$` matches
//   - idempotent: f(f(x)) == f(x)
func TestSanitiseUsernameInvariants(t *testing.T) {
	corpus := []string{
		"", "a", "A", "1", " ", "/", "\\", "user@host",
		"José María", "DOMAIN\\Administrator", "user.name+tag",
		strings.Repeat("é", 100), strings.Repeat("@", 100),
	}
	for _, in := range corpus {
		out := SanitiseUsername(in)
		if got := len(out); got < 1 || got > MaxSanitisedUsernameLen {
			t.Errorf("len out-of-range for %q: got %d", in, got)
		}
		if !expectedShape.MatchString(out) {
			t.Errorf("regex failed for %q → %q", in, out)
		}
		if SanitiseUsername(out) != out {
			t.Errorf("not idempotent: f(%q)=%q but f(f(%q))=%q", in, out, in, SanitiseUsername(out))
		}
	}
}

// FuzzSanitiseUsername hammers the function with arbitrary input and
// confirms the invariants above. Run on CI nightly via `go test -fuzz`.
func FuzzSanitiseUsername(f *testing.F) {
	for _, seed := range []string{
		"", " ", "a", "A", "1", "/", "\\", "user", "Andre Kraus",
		"DOMAIN\\user", "ﬃ", "漢字", strings.Repeat("a", 100),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitiseUsername(in)
		if got := len(out); got < 1 || got > MaxSanitisedUsernameLen {
			t.Fatalf("len out-of-range for %q: got %d (out=%q)", in, got, out)
		}
		if !expectedShape.MatchString(out) {
			t.Fatalf("regex failed: in=%q out=%q", in, out)
		}
		if SanitiseUsername(out) != out {
			t.Fatalf("not idempotent: in=%q out=%q out2=%q", in, out, SanitiseUsername(out))
		}
	})
}
