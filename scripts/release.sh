#!/usr/bin/env bash
#
# Tag a Locorum release.
#
# Validates: tree clean, [Unreleased] CHANGELOG.md section non-empty, every
# release-preflight gate green. Promotes [Unreleased] -> [VERSION] in the
# changelog, commits, tags. Push happens explicitly by the caller.

set -euo pipefail

VERSION=${1:-}
if [ -z "$VERSION" ]; then
    echo "usage: $0 <version>" >&2
    echo "       (semver, e.g. 1.2.3 or 1.2.3-rc1)" >&2
    exit 1
fi

if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.+-]+)?$ ]]; then
    echo "ERROR: $VERSION is not a valid semver tag" >&2
    exit 1
fi

# 1. Tree clean.
if ! git diff --quiet HEAD --; then
    echo "ERROR: working tree has uncommitted changes" >&2
    exit 1
fi
if [ -n "$(git ls-files --others --exclude-standard)" ]; then
    echo "ERROR: working tree has untracked files" >&2
    exit 1
fi

# 2. CHANGELOG.md [Unreleased] non-empty (any non-blank, non-heading line under it).
RELEASE_NOTES=$(awk '
    /^## \[Unreleased\]/ { f=1; next }
    /^## \[/            { if (f) exit }
    f                   { print }
' CHANGELOG.md \
  | grep -vE '^(\s*$|###\s|---)' \
  | head -n 1)

if [ -z "$RELEASE_NOTES" ]; then
    echo "ERROR: [Unreleased] section in CHANGELOG.md is empty" >&2
    exit 1
fi

# 3. release-preflight (lint + vuln + race + cover-check + fuzz + integration + sbom + reproducibility).
echo "==> release-preflight"
make release-preflight

# 4. Move [Unreleased] -> [VERSION] in CHANGELOG.md.
TODAY=$(date -u +%Y-%m-%d)
python3 - "$VERSION" "$TODAY" <<'PY'
import re, sys, pathlib
version, today = sys.argv[1], sys.argv[2]
path = pathlib.Path("CHANGELOG.md")
text = path.read_text()
empty = (
    "## [Unreleased]\n\n"
    "### Added\n### Changed\n### Deprecated\n### Removed\n### Fixed\n### Security\n\n"
    f"## [{version}] - {today}\n"
)
new = re.sub(r"## \[Unreleased\]\n", empty, text, count=1)
if new == text:
    raise SystemExit("ERROR: could not find [Unreleased] heading in CHANGELOG.md")
path.write_text(new)
PY

# 5. Commit + tag.
git add CHANGELOG.md
git commit -m "release: $VERSION"
git tag -a "$VERSION" -m "Locorum $VERSION"

echo ""
echo "OK: tag $VERSION created locally."
echo "Push with:"
echo "  git push origin main"
echo "  git push origin $VERSION"
