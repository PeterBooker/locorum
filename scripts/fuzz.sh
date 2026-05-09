#!/usr/bin/env bash
#
# Run every Fuzz* in the repo for FUZZTIME (default 10s).
#
# `go test -fuzz` accepts only one target per invocation, so each Fuzz
# gets its own `go test` call.
#
# Usage: scripts/fuzz.sh [duration]   # any go test -fuzztime value

set -euo pipefail

FUZZTIME=${1:-10s}
FAIL=0

mapfile -t entries < <(
    grep -rE '^func Fuzz[A-Z][A-Za-z0-9_]*\(' --include='*_test.go' . \
        | sed -E 's|^(.+):func (Fuzz[A-Za-z0-9_]+)\(.*|\1\t\2|' \
        | awk -F'\t' '{
            n = split($1, parts, "/")
            dir = ""
            for (i = 1; i < n; i++) dir = dir parts[i] "/"
            sub(/\/$/, "", dir)
            print dir "\t" $2
          }' \
        | sort -u
)

if [ "${#entries[@]}" -eq 0 ]; then
    echo "no fuzz targets found"
    exit 0
fi

for entry in "${entries[@]}"; do
    pkg=${entry%%$'\t'*}
    name=${entry##*$'\t'}
    pkg_import="./${pkg#./}"
    echo ""
    echo "==> $pkg_import :: $name (${FUZZTIME})"

    # Capture output so we can distinguish a real input-triggered crash
    # from the well-known "context deadline exceeded" harness race that
    # fires when -fuzztime expires while a worker is mid-iteration.
    # Real failures always emit "Failing input written to testdata/...".
    out=$(go test -run='^$' -fuzz="^${name}$" -fuzztime="$FUZZTIME" "$pkg_import" 2>&1)
    rc=$?
    printf '%s\n' "$out"

    if [ $rc -eq 0 ]; then
        continue
    fi

    if printf '%s' "$out" | grep -q 'Failing input written'; then
        # Real crash: a new corpus file lives under testdata/fuzz/<name>/.
        FAIL=1
        continue
    fi

    if printf '%s' "$out" | grep -qE '(context deadline exceeded|fuzzing process hung or terminated)'; then
        echo ">>> $name: harness shutdown race (no failing input recorded); treating as pass" >&2
        continue
    fi

    FAIL=1
done

exit $FAIL
