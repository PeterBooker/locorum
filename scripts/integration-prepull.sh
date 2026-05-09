#!/usr/bin/env bash
#
# Pre-pull every fully-qualified Docker image in internal/version/images.go
# so integration tests don't fight Docker Hub rate limits.

set -euo pipefail

if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker not on PATH" >&2
    exit 1
fi

# Skip *Prefix and *Suffix constants — those are concatenation stubs
# (e.g. "wodby/php:", "-alpine"), not pullable images.
mapfile -t images < <(
    awk -F'"' 'match($0, /^[[:space:]]*([A-Z][A-Za-z0-9]+)[[:space:]]*=[[:space:]]*"/, m) {
        name = m[1]
        if (name ~ /(Prefix|Suffix)$/) next
        print $2
    }' internal/version/images.go | sort -u
)

if [ "${#images[@]}" -eq 0 ]; then
    echo "no images found in internal/version/images.go"
    exit 0
fi

for img in "${images[@]}"; do
    echo "==> docker pull $img"
    docker pull "$img"
done
