#!/usr/bin/env bash
#
# Apply every embedded migration against an in-memory SQLite DB and dump the
# resulting schema. Compares against the checked-in golden in
# internal/storage/testdata/schema/CURRENT.sql. CI fails if they diverge
# without an accompanying migration.

set -euo pipefail

if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "ERROR: sqlite3 not on PATH" >&2
    exit 1
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

DB="$WORK/locorum.db"

# Apply every up.sql in order. Migrations are sorted lexicographically; the
# YYYYMMDDHHMMSS prefix orders them correctly.
for f in $(ls internal/storage/migrations/*.up.sql 2>/dev/null | sort); do
    sqlite3 "$DB" < "$f"
done

GOLDEN="internal/storage/testdata/schema/CURRENT.sql"
mkdir -p "$(dirname "$GOLDEN")"

CURRENT=$(sqlite3 "$DB" '.schema --indent' | sort)

if [ "${UPDATE_GOLDEN:-0}" = "1" ]; then
    echo "$CURRENT" > "$GOLDEN"
    echo "wrote $GOLDEN"
    exit 0
fi

if [ ! -f "$GOLDEN" ]; then
    echo "ERROR: golden schema $GOLDEN missing; run UPDATE_GOLDEN=1 scripts/schema-snapshot.sh"
    exit 1
fi

if ! diff -u "$GOLDEN" <(echo "$CURRENT"); then
    echo ""
    echo "ERROR: schema drift detected. Either:"
    echo "  - add a migration that captures the change, or"
    echo "  - if the change *is* a migration, refresh the golden:"
    echo "      UPDATE_GOLDEN=1 scripts/schema-snapshot.sh"
    exit 1
fi
