#!/usr/bin/env bash
#
# Print per-package coverage from a coverprofile. Run when authoring or
# rebaselining .testcoverage.yml; floor each entry at max(0, observed - 2pp).

set -euo pipefail

PROFILE=${1:-coverage.out}

if [ ! -f "$PROFILE" ]; then
    echo "ERROR: coverage profile $PROFILE not found; run 'make test-cover' first" >&2
    exit 1
fi

go tool cover -func="$PROFILE" \
    | grep -v '^total:' \
    | awk '{
        sub(/:[0-9]+:.*$/, "", $1)
        sub(/[^/]+\.go$/, "", $1)
        sub(/\/$/, "", $1)
        gsub(/%/, "", $3)
        sum[$1] += $3
        cnt[$1]++
      }
      END {
        for (p in sum) printf "%-60s %.1f\n", p, sum[p]/cnt[p]
      }' \
    | sort

echo ""
echo "Floor suggestion = max(0, floor(observed - 2))."
