#!/usr/bin/env bash
#
# Pre-pull every fully-qualified Docker image in internal/version/images.go
# so integration tests don't fight Docker Hub rate limits.

set -euo pipefail

if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker not on PATH" >&2
    exit 1
fi

# Skip the *Prefix entries that end in ":" — those are concatenation
# stubs, not pullable images.
mapfile -t images < <(
    awk -F'"' '/^\s*[A-Z][A-Za-z0-9]+\s*=\s*"/ {
        v = $2
        if (v ~ /:$/) next
        print v
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
