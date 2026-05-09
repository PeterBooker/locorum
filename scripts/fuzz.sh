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
    if ! go test -run='^$' -fuzz="^${name}$" -fuzztime="$FUZZTIME" "$pkg_import"; then
        FAIL=1
    fi
done

exit $FAIL
