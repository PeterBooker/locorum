#!/usr/bin/env bash
#
# Run govulncheck and filter against a small allowlist of reviewed,
# unfixable upstream advisories. Any vulnerability ID NOT in the
# allowlist fails the build. New IDs that appear in the future will
# fire — that is the point.
#
# To accept a new ID, add it below with a comment explaining why we
# can't/shouldn't fix it (typically: upstream has no fix and the code
# path is not actually reachable from this consumer).

set -euo pipefail

if ! command -v govulncheck >/dev/null 2>&1; then
    echo "govulncheck not on PATH; install with: go install golang.org/x/vuln/cmd/govulncheck@latest" >&2
    exit 1
fi

# Allowlisted vulnerability IDs. Each entry MUST have a comment.
ALLOWLIST=(
    # GO-2026-4887: Moby AuthZ plugin bypass on oversized request bodies.
    # Daemon-side vulnerability in moby/moby. The Docker Go SDK shares
    # the `api` package, so govulncheck flags every SDK consumer, but
    # the affected code path is server-side only (the daemon's AuthZ
    # plugin chain). Locorum is a client; not reachable. No SDK fix
    # exists and the upstream CVE is fixed only in moby/moby itself.
    "GO-2026-4887"
    # GO-2026-4883: Moby off-by-one in plugin privilege validation.
    # Same rationale as GO-2026-4887 — daemon-side, no SDK fix.
    "GO-2026-4883"
)

# Build a regex of allowlisted IDs.
allow_re=$(printf '|%s' "${ALLOWLIST[@]}")
allow_re=${allow_re:1}  # strip leading '|'

# Capture output (exit 3 = vulnerabilities found; exit 0 = clean).
out=$(govulncheck ./... 2>&1) || true

# Extract every reported CVE id, dedupe.
all_ids=$(printf '%s\n' "$out" | grep -oE 'GO-[0-9]+-[0-9]+' | sort -u || true)

if [ -z "$all_ids" ]; then
    echo "govulncheck: no vulnerabilities found"
    exit 0
fi

# IDs not in allowlist.
unexpected=$(printf '%s\n' "$all_ids" | grep -vE "^(${allow_re})$" || true)

if [ -n "$unexpected" ]; then
    printf '%s\n' "$out"
    echo ""
    echo "ERROR: govulncheck found unallowlisted vulnerabilities:"
    printf '  %s\n' $unexpected
    echo ""
    echo "Either upgrade the affected dependency or, if the advisory"
    echo "is genuinely unactionable, add the ID to scripts/vuln-check.sh"
    echo "with a comment explaining why."
    exit 1
fi

echo "govulncheck: only allowlisted vulnerabilities present:"
printf '  %s\n' $all_ids
