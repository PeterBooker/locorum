# Pre-tag release smoke checklist

Run this against a freshly-built `make build` binary before tagging
a release. Every box must be checked; the result is captured in the
release PR description (TESTING.md §3.12.4).

## Setup

- [ ] `git status` is clean.
- [ ] `make ci` is green.
- [ ] `make integration` is green (Linux, real Docker).
- [ ] `make release-preflight` is green.
- [ ] `CHANGELOG.md` `[Unreleased]` section has at least one entry.

## Cold-launch sanity

- [ ] App launches; sidebar lists existing sites without errors.
- [ ] No errors in `~/.locorum/logs/locorum.log` from the launch.

## Lifecycle (golden path)

- [ ] **New site**: create with default settings; toast shows green dot
      within 60s.
- [ ] **Open in browser**: WP installer renders.
- [ ] **WP install**: complete via wp-cli panel; admin login works.
- [ ] **Stop**: containers exit; volume name visible in `docker volume ls`.
- [ ] **Start**: WP still installed; logged-in session preserved.
- [ ] **Clone**: cloned site starts; clone has identical content.
- [ ] **Snapshot**: snapshot listed; restore reverses a `wp_options` change.
- [ ] **Delete (keep DB)**: containers gone, volume preserved.
- [ ] **Delete (purge)**: volume gone too.

## Edge cases

- [ ] **Multisite**: subdomain enabled; `site1.<slug>.localhost` resolves.
- [ ] **Version change**: PHP 8.3 → 8.4 succeeds; `phpinfo()` reports new.
- [ ] **Hooks**: pre-start `exec` hook fires; output streams to UI.
- [ ] **Import**: import-DB modal accepts `.sql.gz`; site URLs auto-rewrite.

## Cross-platform spot check

- [ ] Linux (current daily driver): all of the above.
- [ ] macOS arm64: launch + create + start (DMG-installed binary).
- [ ] Windows: launch + create + start (MSI-installed binary).

## Process

- [ ] Quit and relaunch — UI state restored; no orphan containers.
- [ ] Run `make sbom`; review CycloneDX output for unexpected dependencies.
- [ ] Run `make vuln`; resolve any HIGH/CRITICAL findings before tagging.
- [ ] Run `./scripts/release.sh X.Y.Z`; check `git tag -l` shows the tag.

Sign off: `<your name>` on `<date>`.
