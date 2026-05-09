#!/usr/bin/env bash
#
# Bench HEAD vs BENCH_BASE (default origin/main), benchstat-diff, fail
# above BENCH_TOLERANCE% regression (default 20%).

set -euo pipefail

TOLERANCE=${BENCH_TOLERANCE:-20}
BASE=${BENCH_BASE:-origin/main}

if ! command -v benchstat >/dev/null 2>&1; then
    echo "installing benchstat"
    go install golang.org/x/perf/cmd/benchstat@latest
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Capture HEAD's bench output first so we don't disturb the user's tree.
echo "==> bench HEAD"
go test -bench=. -benchmem -count=10 -run='^$' -benchtime=1s ./... > "$WORK/pr.bench" 2>&1 || true

# Stash + checkout base.
HEAD_REF=$(git rev-parse --abbrev-ref HEAD)
STASHED=0
if ! git diff --quiet HEAD --; then
    git stash push --include-untracked -m "bench-compare auto-stash" >/dev/null
    STASHED=1
fi
trap 'git checkout -q "$HEAD_REF"; if [ "$STASHED" = 1 ]; then git stash pop -q || true; fi; rm -rf "$WORK"' EXIT

git checkout -q "$BASE"

echo "==> bench $BASE"
go test -bench=. -benchmem -count=10 -run='^$' -benchtime=1s ./... > "$WORK/main.bench" 2>&1 || true

git checkout -q "$HEAD_REF"
if [ "$STASHED" = 1 ]; then
    git stash pop -q
    STASHED=0
fi

echo "==> benchstat"
benchstat "$WORK/main.bench" "$WORK/pr.bench" | tee "$WORK/delta.txt"

# Fail if any benchmark regresses by > TOLERANCE%. benchstat reports deltas
# like `+12.34%`. Strip everything else, parse, compare.
if awk -v tol="$TOLERANCE" '
    /^[A-Za-z]/ {
        for (i=1; i<=NF; i++) {
            if ($i ~ /^\+[0-9]+(\.[0-9]+)?%$/) {
                gsub(/[+%]/, "", $i)
                if ($i + 0 > tol) {
                    printf "REGRESSION on %s: +%.2f%% (tolerance %d%%)\n", $1, $i, tol
                    exit 1
                }
            }
        }
    }
' "$WORK/delta.txt"; then
    echo "OK: no regressions above ${TOLERANCE}% tolerance"
else
    exit 1
fi
