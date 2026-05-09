package sites

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Worktree slug derivation per AGENTS-SUPPORT.md D3.
//
// The naive concatenation `<parent-slug>-<branch-slug>.localhost`
// breaks for branches like `feature/long-descriptive-name` (slash is
// illegal in DNS labels per RFC 1035, and the combined length blows
// past the 63-char label limit on real branches). The chosen scheme:
//
//     <parent-slug>-<safe-branch[:18]>-<hash6>
//
// where:
//   - safe-branch lower-cases the branch, replaces every non-[a-z0-9]
//     run with a single dash, trims leading/trailing dashes, then
//     truncates at 18 chars.
//   - hash6 is the first 6 lowercase hex chars of
//     sha256(parent-slug || \x00 || branch || \x00 || worktree-path).
//     Including the worktree path means two worktrees of the same
//     branch checked out at different host paths still resolve to
//     distinct slugs, which matters for an agent juggling parallel
//     experiments.
//
// The output is always under the 63-char DNS-label limit and contains
// only lowercase letters, digits, and single dashes.

// worktreeSlugBranchMax is the upper bound on the branch fragment baked
// into the derived slug. 18 chars is a balance: long enough that humans
// can recognise their branch by name, short enough to fit even when
// combined with a 30-char parent slug.
const worktreeSlugBranchMax = 18

// worktreeHashLen is the number of hex chars from the sha256 digest
// included in the derived slug. 6 hex chars (24 bits) means 1-in-16M
// collision per branch — adequate when the parent + branch already
// disambiguate at the human level.
const worktreeHashLen = 6

// dnsLabelMax is the RFC 1035 limit on a single DNS label. We never
// emit a slug longer than this; the parent fragment is truncated if
// the combined length would otherwise exceed it.
const dnsLabelMax = 63

// WorktreeSlug returns the derived slug for a worktree-bound site.
// Pure function — same inputs always produce the same output, even
// across processes and Locorum versions.
//
// parentSlug must already be a valid Locorum slug (lowercase, hyphens
// only); the function does not re-sanitise it. branch and worktreePath
// are taken verbatim and may contain any printable runes.
func WorktreeSlug(parentSlug, branch, worktreePath string) string {
	hash := sha256.New()
	hash.Write([]byte(parentSlug))
	hash.Write([]byte{0})
	hash.Write([]byte(branch))
	hash.Write([]byte{0})
	hash.Write([]byte(worktreePath))
	hashHex := hex.EncodeToString(hash.Sum(nil))[:worktreeHashLen]

	safeBranch := sanitiseBranchForSlug(branch)
	if safeBranch == "" {
		safeBranch = "branch"
	}
	if len(safeBranch) > worktreeSlugBranchMax {
		safeBranch = safeBranch[:worktreeSlugBranchMax]
		// Re-trim a trailing dash created by the truncation so we
		// don't emit "-x--abc123".
		safeBranch = strings.TrimRight(safeBranch, "-")
		if safeBranch == "" {
			safeBranch = "branch"
		}
	}

	// Compose. Cap parent fragment if needed to fit 63 chars total.
	const sepLen = 2 // two single dashes between the three fragments
	maxParent := dnsLabelMax - len(safeBranch) - worktreeHashLen - sepLen
	if maxParent < 1 {
		// Pathological: a 60-char branch fragment leaves nothing for
		// the parent. Fall back to a hash-only label so the output is
		// at least valid.
		return safeBranch + "-" + hashHex
	}
	pruned := parentSlug
	if len(pruned) > maxParent {
		pruned = strings.TrimRight(pruned[:maxParent], "-")
		if pruned == "" {
			pruned = "site"
		}
	}
	return pruned + "-" + safeBranch + "-" + hashHex
}

// WorktreeDomain returns the .localhost domain for a worktree slug.
// Wraps WorktreeSlug — kept separate so calls that need both don't
// recompute the hash.
func WorktreeDomain(parentSlug, branch, worktreePath string) string {
	return WorktreeSlug(parentSlug, branch, worktreePath) + ".localhost"
}

// sanitiseBranchForSlug lowercases branch and replaces every run of
// non-[a-z0-9] characters with a single dash. Leading/trailing dashes
// are trimmed. Returns "" only when the input contained no usable
// alphanumerics.
//
// The implementation is deliberately stricter than git's branch-name
// rules (which allow `/`, `+`, `.`, etc.) because the output flows
// straight into a DNS label. ASCII-only by design — non-ASCII runes
// get stripped to dashes so a branch named "fix/é" still produces a
// valid label.
func sanitiseBranchForSlug(branch string) string {
	var b strings.Builder
	b.Grow(len(branch))
	prevDash := true // suppress leading dash by starting "as if" we just emitted one
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// ValidateWorktreeOpts is a defensive check on the user-supplied
// inputs to CreateWorktreeSite. Rejects empty values and obviously-
// hostile patterns (NUL byte, .. traversal in worktreePath). Surfaces
// a user-readable error so the CLI can print it directly.
func ValidateWorktreeOpts(remote, branch, worktreePath string) error {
	if strings.TrimSpace(remote) == "" {
		return errors.New("git remote is required")
	}
	if strings.TrimSpace(branch) == "" {
		return errors.New("git branch is required")
	}
	if strings.ContainsRune(remote, 0) || strings.ContainsRune(branch, 0) || strings.ContainsRune(worktreePath, 0) {
		return errors.New("input contains NUL byte")
	}
	if strings.Contains(worktreePath, "..") {
		// Worktree path is bind-mounted into the PHP/web container.
		// `..` segments are rarely intentional; reject them to keep
		// the surface narrow.
		return fmt.Errorf("worktreePath must not contain %q", "..")
	}
	return nil
}
