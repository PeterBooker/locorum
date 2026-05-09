#!/usr/bin/env bash
#
# Build the binary twice, byte-compare. Fails if the two artifacts differ.
# Pinned to the host Linux toolchain — see docs/BUILDING.md for the
# supported environment. Cross-platform reproducibility is not promised.

set -euo pipefail

VERSION=${VERSION:-test-repro-$(date +%s)}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

VERSION_PKG="github.com/PeterBooker/locorum/internal/version"
SOURCE_DATE_EPOCH=$(git log -1 --pretty=%ct)
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date -u -d @"$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w \
  -X $VERSION_PKG.Version=$VERSION \
  -X $VERSION_PKG.Commit=$COMMIT \
  -X $VERSION_PKG.Date=$DATE"

build_one() {
    local out=$1
    mkdir -p "$out"
    GOFLAGS='-trimpath -buildvcs=false' \
    SOURCE_DATE_EPOCH="$SOURCE_DATE_EPOCH" \
    CGO_ENABLED=1 \
        go build -ldflags "$LDFLAGS" -o "$out/locorum" .
}

echo "==> build A"
build_one "$WORK/a"
echo "==> build B"
build_one "$WORK/b"

if cmp -s "$WORK/a/locorum" "$WORK/b/locorum"; then
    SHA=$(sha256sum "$WORK/a/locorum" | cut -d' ' -f1)
    echo "OK: builds reproducible (sha256: $SHA)"
    exit 0
fi

echo "::error::builds differ"
sha256sum "$WORK/a/locorum" "$WORK/b/locorum"
if command -v diffoscope >/dev/null 2>&1; then
    diffoscope "$WORK/a/locorum" "$WORK/b/locorum" || true
else
    echo "(install diffoscope for a deeper byte-diff report)"
fi
exit 1
