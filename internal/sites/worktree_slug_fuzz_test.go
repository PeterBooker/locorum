package sites

import (
	"strings"
	"testing"
)

// Asserts the output invariants documented in worktree_slug.go.
func FuzzWorktreeSlug(f *testing.F) {
	seeds := [][3]string{
		{"site", "main", "/tmp/wt"},
		{"long-parent-name-with-dashes", "feature/long-descriptive-branch", "/srv/wt"},
		{"site", "", "/tmp"},
		{"site", "../../../etc/passwd", "/"},
		{"site", "release/v1.2.3", "/var/wt"},
		{"site", "feature/é-café", "/tmp/wt"},
		{"site", strings.Repeat("a", 200), "/"},
		{strings.Repeat("z", 200), "main", "/"},
		{"site", "0", "/"},
		{"site", "____", "/"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, parent, branch, path string) {
		// WorktreeSlug's contract assumes parent is already a valid
		// slug; behaviour on garbage parents is undefined.
		if !isValidParentSlug(parent) {
			t.Skip("invalid parent violates WorktreeSlug precondition")
		}
		out := WorktreeSlug(parent, branch, path)
		if out == "" {
			t.Fatalf("empty slug from parent=%q branch=%q path=%q", parent, branch, path)
		}
		if len(out) > dnsLabelMax {
			t.Fatalf("slug exceeds DNS label limit (%d > %d): %q", len(out), dnsLabelMax, out)
		}
		if strings.HasPrefix(out, "-") || strings.HasSuffix(out, "-") {
			t.Fatalf("slug has leading/trailing dash: %q", out)
		}
		for i, r := range out {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Fatalf("slug %q has invalid rune %q at offset %d", out, r, i)
			}
		}
		if strings.Contains(out, "--") {
			t.Fatalf("slug %q has consecutive dashes", out)
		}
		if out2 := WorktreeSlug(parent, branch, path); out != out2 {
			t.Fatalf("non-deterministic: %q vs %q", out, out2)
		}
	})
}

func isValidParentSlug(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			return false
		}
		if r == '-' && prevDash {
			return false
		}
		prevDash = r == '-'
	}
	return true
}

func FuzzValidateWorktreeOpts(f *testing.F) {
	seeds := [][3]string{
		{"https://github.com/x/y", "main", "/tmp/wt"},
		{"", "main", "/tmp/wt"},
		{"https://x", "", "/tmp/wt"},
		{"https://x", "main", ""},
		{"https://x", "main", "../../etc"},
		{"http\x00s://x", "main", "/tmp"},
	}
	for _, s := range seeds {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, remote, branch, path string) {
		err := ValidateWorktreeOpts(remote, branch, path)
		if err != nil {
			return
		}
		if strings.ContainsRune(remote, 0) || strings.ContainsRune(branch, 0) || strings.ContainsRune(path, 0) {
			t.Fatalf("expected NUL byte rejection: remote=%q branch=%q path=%q", remote, branch, path)
		}
		if strings.Contains(path, "..") {
			t.Fatalf("expected .. rejection in worktreePath: %q", path)
		}
		if strings.TrimSpace(remote) == "" || strings.TrimSpace(branch) == "" {
			t.Fatalf("expected empty remote/branch rejection")
		}
	})
}
