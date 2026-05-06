package sites

import (
	"strings"
	"testing"
)

func TestWorktreeSlug_Deterministic(t *testing.T) {
	a := WorktreeSlug("shop", "feature/x", "/home/me/work/shop/.worktrees/feature-x")
	b := WorktreeSlug("shop", "feature/x", "/home/me/work/shop/.worktrees/feature-x")
	if a != b {
		t.Fatalf("WorktreeSlug not deterministic: %q vs %q", a, b)
	}
}

func TestWorktreeSlug_DifferentInputsCollideRarely(t *testing.T) {
	cases := []struct {
		parent, branch, path string
	}{
		{"shop", "feature/x", "/a"},
		{"shop", "feature/y", "/a"},
		{"shop", "feature/x", "/b"},
		{"site", "feature/x", "/a"},
	}
	seen := map[string]struct{}{}
	for _, c := range cases {
		s := WorktreeSlug(c.parent, c.branch, c.path)
		if _, dup := seen[s]; dup {
			t.Fatalf("collision for %+v: %s", c, s)
		}
		seen[s] = struct{}{}
	}
}

func TestWorktreeSlug_DNSLabelLimit(t *testing.T) {
	long := strings.Repeat("verylongbranch", 5) // 70 chars
	s := WorktreeSlug("very-long-parent-slug-name", long, "/somewhere")
	if len(s) > 63 {
		t.Fatalf("slug exceeds DNS label limit (63): len=%d slug=%q", len(s), s)
	}
}

func TestWorktreeSlug_OnlyAllowedChars(t *testing.T) {
	s := WorktreeSlug("shop", "feature/É+thing--with junk", "/x")
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			t.Fatalf("unexpected rune %q in slug %q", r, s)
		}
	}
	// Branch fragment should still be recognisable.
	if !strings.Contains(s, "feature") {
		t.Fatalf("branch fragment lost: %q", s)
	}
}

func TestWorktreeSlug_BranchEntirelyInvalidFallsBack(t *testing.T) {
	s := WorktreeSlug("shop", "💩///", "/x")
	if !strings.Contains(s, "branch") {
		t.Fatalf("expected fallback branch fragment, got %q", s)
	}
}

func TestWorktreeSlug_NoLeadingOrTrailingDashes(t *testing.T) {
	s := WorktreeSlug("shop", "/feature/x/", "/x")
	if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		t.Fatalf("dashes at edges of %q", s)
	}
	// And no consecutive dashes.
	if strings.Contains(s, "--") {
		t.Fatalf("consecutive dashes in %q", s)
	}
}

func TestValidateWorktreeOpts(t *testing.T) {
	cases := []struct {
		name             string
		remote, br, path string
		wantErr          bool
	}{
		{"happy", "git@x:y", "main", "/home/p/work", false},
		{"empty remote", "", "main", "", true},
		{"empty branch", "git@x:y", "", "", true},
		{"nul byte", "git@x:y", "ma\x00in", "", true},
		{"traversal", "git@x:y", "main", "../escape", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWorktreeOpts(tc.remote, tc.br, tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v want=%v", err, tc.wantErr)
			}
		})
	}
}
