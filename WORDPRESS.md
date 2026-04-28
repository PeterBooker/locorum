# WordPress Implementation Plan

Distillation of the WordPress-specific findings in `LEARNINGS.md` (the DDEV
deep-dive) into a concrete implementation plan for Locorum. Locorum is
WordPress-only and GUI-first, so the WP layer can — and should — be more
opinionated than DDEV's CMS-agnostic abstraction.

The points below are pulled from `LEARNINGS.md` §2.3, §3.1–§3.10, §4.3–§4.4,
and the related footguns in §9. Section numbers in parentheses point back to
the source for context.

## Status snapshot (2026-04-27)

What already exists:

- `internal/sites/wordpress.go` — downloads `wordpress.org/latest.tar.gz`,
  extracts into `site.FilesDir`/`PublicDir`. No `wp-config.php` writing.
- `internal/sites/sites.go:458` `ensureMultisite` — runs `wp core install`
  then `wp core multisite-convert`. No PHP constants set, no nginx multisite
  rewrites, no wildcard host/cert.
- `internal/sites/sites.go:933` — search-replace already runs on **clone**.
  No equivalent on DB import (because `ImportDB` doesn't exist yet).
- `internal/hooks/events.go:38` — `PreImportDB` / `PostImportDB` constants
  are declared and reserved; **no firing site exists**. Same applies to the
  reserved snapshot events.
- `internal/sites/sites.go` `ExecWPCLI` exists; `internal/ui/wpcli.go` is
  wired through. The PHP image is `wodby/php:{version}` which ships wp-cli.

Conclusion: most WP-specific behaviour from `LEARNINGS.md` is **not yet
implemented**. The hooks engine is in place, so each lifecycle action below
slots into the existing `runHooks(ctx, …)` pattern.

## 1. `wp-config.php` split with signed regen (§3.1, §2.3, F9)

DDEV writes `wp-config.php` once (with `#ddev-generated`) and a
freely-regenerated `wp-config-ddev.php`. The first file `require`s the
second; the include guard `!defined('DB_USER')` lets a user-edited
`wp-config.php` win on conflict.

**Plan**

1. New file `internal/sites/wpconfig.go` with:
   - `writeWPConfig(site)` — renders embedded template to
     `{filesDir}/{publicDir}/wp-config.php` only if the file is missing or
     contains `// #locorum-generated:` on the first non-empty line. If
     missing the signature, log a warning and emit a UI toast pointing at the
     manual snippet to paste.
   - `writeWPConfigLocorum(site)` — always rewrites
     `wp-config-locorum.php` (signature in the header). Idempotent:
     compute the rendered byte-string, compare to existing file, skip write
     on match (per §1.6 idempotent writes).
2. Embed both templates under `config/wordpress/` via `go:embed`:
   - `wp-config.tmpl.php` — salts (regenerated per site), charset, collation,
     `require_once __DIR__.'/wp-config-locorum.php';`, then the standard WP
     boilerplate. **Salts come from `wordpress.org/secret-key/1.1/salt/` at
     site creation, cached in the DB row** so they survive regenerates.
   - `wp-config-locorum.tmpl.php` — DB credentials, `WP_HOME`, `WP_SITEURL`,
     `WP_DEBUG`, `WP_DEBUG_LOG`, multisite constants if applicable.
3. New step `WriteWPConfigStep` in `internal/sites/sitesteps/`. Insert into
   `StartSite`'s plan after `EnsureNetworkStep` and before
   `CreateContainersStep`. Step is idempotent on re-run.
4. `types.Site` gains a `Salts string` field (a single JSON blob — easier
   than 8 columns). Migration: `add_wp_salts.up.sql` /  `.down.sql` per the
   `add-migration` skill.
5. Honour the user-removed-signature path: if the user strips the comment
   from `wp-config.php`, never overwrite. `wp-config-locorum.php` is always
   ours; document this distinction in the in-app help.

**Footgun coverage**: F9 (wp-config clobber).

## 2. Dynamic URLs via env vars (§3.2)

DDEV resolves the primary URL at PHP request time from an env var. Changing
the domain in the GUI just recreates the container with new env — zero PHP
file rewriting.

**Plan**

1. Add to the PHP container `ContainerSpec` (in `internal/docker/specs.go`
   or wherever `PHPSpec` is built):
   ```
   LOCORUM_PRIMARY_URL = "https://" + site.Domain
   LOCORUM_DOCROOT     = site.PublicDir       // "" or "wordpress" etc.
   LOCORUM_APPROOT     = "/var/www/html"
   LOCORUM_SITE_SLUG   = site.Slug
   ```
   These values feed the `EnsureContainer` config-hash, so a domain change
   forces a recreate (correct behaviour).
2. `wp-config-locorum.tmpl.php` reads them:
   ```php
   if (!defined('WP_HOME'))    define('WP_HOME', getenv('LOCORUM_PRIMARY_URL') ?: 'http://localhost');
   if (!defined('WP_SITEURL')) define('WP_SITEURL', WP_HOME . (getenv('LOCORUM_DOCROOT') ? '/' . getenv('LOCORUM_DOCROOT') : ''));
   ```
3. UI: change-domain action (already exposed in `versioneditor.go`?
   verify) becomes "edit Site fields, recreate PHP container". No file
   rewriting.

**Why not put the URL in the file directly?** It would need a rewrite on
every domain change, and any `wp-cli` invocation that introspects URLs
would race with file write. Env-var resolution is atomic.

## 3. WordPress detection two levels deep (§3.3, F6)

DDEV looks for `wp-settings.php` in the docroot or one level deeper. Multiple
matches → fail loudly.

**Plan**

1. `internal/sites/wordpress.go` gains `detectWordPress(filesDir,
   publicDir)`:
   - Look for `wp-settings.php` at `{publicDir}/` and at depth-1
     subdirectories of `{publicDir}/`.
   - Return `(found []string, err)`. Caller treats `len > 1` as a hard
     error (a config issue, not a bug to paper over).
2. Used by:
   - "Adopt existing directory" flow (when added — currently we always
     download fresh).
   - Pre-start sanity check: refuses to start a site whose tree no longer
     contains a recognisable WordPress install. Surface as a typed error in
     the UI (per §7.1).
3. Test: table-driven cases — empty dir, valid dir, wp/ subdir, two
   conflicting dirs.

**Footgun coverage**: F6 (`wp-settings.php` ambiguity).

## 4. WP-CLI is already there — formalise the wrapper (§3.4)

`wodby/php` ships wp-cli. We already exec `wp …` from `clone` and
`multisite`. Pull this into one place.

**Plan**

1. New file `internal/sites/wpcli.go` (note: `internal/ui/wpcli.go` exists
   for the panel — different layer). Sketch:
   ```go
   func (sm *SiteManager) wpcli(ctx, site, args...) (string, error)
   func (sm *SiteManager) wpInstall(ctx, site) error                 // wp core install
   func (sm *SiteManager) wpSearchReplace(ctx, site, from, to) error
   func (sm *SiteManager) wpDBImport(ctx, site, hostPath) error
   func (sm *SiteManager) wpOptionGet/Update(ctx, site, key, val) error
   ```
2. Replace the inline `wp …` exec calls in `sites.go:920-936` (the clone
   path) and `sites.go:461-487` (the multisite path) with these helpers.
3. Helpers always set `--allow-root` and pin `--path={LOCORUM_DOCROOT}`
   when present — avoids surprises when wp-cli auto-detects from CWD.

This is foundational for §5 (DB import), §6 (post-install hooks), and §7
(multisite).

## 5. DB import with auto search-replace (§3.5, §4.3, §4.4, F16)

Today there is no `ImportDB`. The two reserved hook events
(`PreImportDB`/`PostImportDB`) have no firing site. The single biggest
WordPress UX win in the entire `LEARNINGS.md` document.

**Plan**

1. New method `SiteManager.ImportDB(ctx, siteID, hostPath, opts)`:
   - Hold `siteMutex(siteID)` for the duration.
   - `runHooks(ctx, hooks.PreImportDB, site)`.
   - Build an `orch.Plan` of:
     1. `DetectArchiveFormatStep` — `.sql`, `.sql.gz`, `.sql.bz2`,
        `.sql.xz`, `.zip` (single-file only — error on multi-file zip).
     2. `StreamPreprocessStep` — copy host file to a temp file inside the
        site bind-mount, decompressing on the fly. Apply the
        stripping-regex set:
        - drop `CREATE DATABASE …;` / `USE …;` lines.
        - drop MariaDB 11.x `utf8mb4_uca1400_*` collations
          (replace with `utf8mb4_unicode_ci`).
        - drop `/*!999999\\- ... */` "sandbox mode" wrappers.
        - drop `DEFINER=` clauses that reference users we don't have.
        Implement in Go via `bufio.Scanner` + `regexp` (no shelling to
        `perl`/`sed` — keeps Windows clean). Live in
        `internal/sites/import_filters.go` so the regex list is one
        focused, well-tested file. New filters land here when users hit
        new export quirks.
     3. `WPDBImportStep` — `wp db import /var/www/html/<file>
        --allow-root` via the helper from §4.
     4. `AutoSearchReplaceStep` — runs **only if** `opts.SourceURLs` is
        set or auto-detected: scan the imported dump's `siteurl` /`home`
        options before import (or `wp option get` after) and run
        `wp search-replace <from> <to> --all-tables --skip-columns=guid`
        for every URL the user opted in to. **Always** include the bare
        host variant and the `https://` / `http://` pair.
     5. `CleanupStep` — remove the temp dump.
   - `runHooks(ctx, hooks.PostImportDB, site)`.
2. UI: import wizard in `internal/ui/dbimport.go` (new). Three screens:
   - File picker (gated on supported extension).
   - URL mapping table with sensible defaults (`https://prod.example.com`
     → `https://{site.Domain}`). Defaults seeded by reading
     `wp_options.{siteurl,home}` from the dump if we can extract it
     cheaply (mysql `--skip-comments | grep "INSERT INTO`wp_options`"`
     style — defer if it's painful).
   - Progress + log tail.
3. Footgun coverage: F16 (redirect loop), and the import-filter list
   covers F7 (collation incompatibility).

**Out of scope (phase 3)**: hosting-provider direct pull from Kinsta /
WP Engine / Pressable. Per LEARNINGS roadmap item #24.

## 6. WordPress lifecycle hook points (§3.6)

The hooks engine ships generic events. WP wants opinionated default
behaviours that fire on those events out of the box (the user's hooks then
run *around* them).

**Plan**

A hook is user-configured. The behaviours below are *internal steps*, not
hooks — they run unconditionally for WordPress sites and bracket the user
hooks correctly:

1. **PostStart-internal**: if `wp_options.siteurl` is empty / table missing
   → `wp core install` with the defaults from `ensureMultisite` (admin /
   admin / admin@domain). Today this lives inside `ensureMultisite`; lift
   it out so single-site installs benefit too.
2. **PostImportDB-internal**: search-replace (§5).
3. **PostImportFiles-internal**: chmod `wp-content/uploads` to 0777
   (matches the §1.4 ownership-fixup pattern; runs in the alpine
   one-shot). Triggered from a future `ImportFiles` flow — for now a TODO
   comment is enough.
4. **PreDelete-internal**: auto-snapshot. Implements §4.2 — write
   `~/.locorum/snapshots/{slug}_pre_delete_{ts}.sql.zst` before the delete
   plan tears volumes down. Add a `RestoreSnapshot(ctx, slug, file)`
   companion so the pre-delete promise is real.

Order of execution per lifecycle method (e.g. `ImportDB`):
```
runHooks(PreImportDB)   // user hooks
…internal step…         // search-replace etc.
runHooks(PostImportDB)  // user hooks
```
This matches the "internal behaviour is not a hook" rule and keeps user
hooks composable.

## 7. Multisite as a first-class feature (§3.7)

Currently we run `wp core multisite-convert` and stop. Missing: PHP
constants, wildcard hostname registration, wildcard cert, per-site nginx
multisite rewrites.

**Plan**

1. `wp-config-locorum.tmpl.php` — when `site.Multisite != ""`, emit:
   ```php
   define('WP_ALLOW_MULTISITE', true);
   define('MULTISITE', true);
   define('SUBDOMAIN_INSTALL', {{.Subdomain}});  // bool
   define('DOMAIN_CURRENT_SITE', '{{.Domain}}');
   define('PATH_CURRENT_SITE',   '/');
   define('SITE_ID_CURRENT_SITE', 1);
   define('BLOG_ID_CURRENT_SITE', 1);
   ```
2. Routing — extend `router.UpsertSite` so the `SiteRoute` carries
   wildcards: `*.{site.Domain}` for subdomain installs, `{site.Domain}`
   only for subdirectory. Traefik config: `HostRegexp` rule. Update
   `internal/router/traefik/`. The `router.fake` and the route-validation
   in §2.7 of `LEARNINGS.md` need wildcard-aware fixtures.
3. mkcert wildcard cert — when `internal/tls/mkcert.go` generates a cert
   for a multisite-subdomain site, request both `{domain}` and `*.{domain}`
   in one cert.
4. nginx — `config/nginx/site.conf.tmpl` adds the WP multisite rewrite
   block guarded by a template var:
   ```nginx
   {{if .MultisiteSubdir}}
   if (!-e $request_filename) {
     rewrite /wp-admin$ $scheme://$host$uri/ permanent;
     rewrite ^(/[^/]+)?(/wp-.*)         $2 last;
     rewrite ^(/[^/]+)?(/.*\.php)$      $2 last;
   }
   {{end}}
   ```
   Subdomain installs don't need the rewrites.
5. UI — the new-site modal already has a multisite dropdown (per
   `add-multisite` migration). Surface a "subdomain installs require
   wildcard DNS" hint when `*.localhost` is in use (every modern OS
   resolves it; no /etc/hosts work needed).

**Note**: do *not* copy DDEV's "let user manage multisite" stance. Locorum
is WP-only — opinionated defaults are the point.

## 8. HTTPS detection via reverse proxy (§3.9)

Plugins/themes that read `$_SERVER['HTTPS']` directly will see HTTP on the
PHP container because Traefik terminates TLS. WP core respects
`X-Forwarded-Proto`; many third-party plugins don't.

**Plan**

1. `wp-config-locorum.tmpl.php` emits the canonical handler at the top:
   ```php
   if (!empty($_SERVER['HTTP_X_FORWARDED_PROTO']) && $_SERVER['HTTP_X_FORWARDED_PROTO'] === 'https') {
       $_SERVER['HTTPS'] = 'on';
   }
   ```
2. Confirm Traefik dynamic config sets the header for both per-site and
   service routes. Add an integration test (when §8.1 lands) that hits the
   site and asserts `is_ssl()` is true.

No new files; this is a one-liner in the template plus a verified header.

## 9. WP_DEBUG by default + log tail panel (§3.10)

Default `WP_DEBUG=true` for local dev, route the log file, and surface a
tail in the UI.

**Plan**

1. `wp-config-locorum.tmpl.php`:
   ```php
   define('WP_DEBUG',         true);
   define('WP_DEBUG_LOG',     '/var/www/html/wp-content/debug.log');
   define('WP_DEBUG_DISPLAY', false);
   define('SCRIPT_DEBUG',     true);
   ```
   Override-overrides via env if a user really wants `WP_DEBUG=false`
   locally (rare).
2. New panel `internal/ui/debuglog.go` — tail
   `{filesDir}/{publicDir}/wp-content/debug.log` via the existing
   log-streamer abstraction in `internal/ui/logviewer.go`. Add a tab
   alongside the container logs in `sitedetail.go`.
3. `hookeditor.go:336` already shows `tail -n 50 wp-content/debug.log` as
   an example — keep that example, the panel is the GUI version.

## 10. Bedrock support (§3.8) — phase 3

Out of scope for v1. Architecture notes so the WP layer doesn't paint us
into a corner:

- The "lifecycle hook points" abstraction (§6 above) is the seam. Extract
  WP defaults into a `wpFlavour` interface (`Standard{}`, `Bedrock{}`)
  with methods `ConfigPaths(site) []GeneratedFile`, `Detect(dir) (ok
  bool)`, `EnvVars(site) map[string]string`, `WPCLIArgs(site) []string`.
- Standard flavour writes `wp-config.php` + `wp-config-locorum.php`.
  Bedrock writes `.env` + reads `config/application.php`. Detection runs
  in `detectWordPress` (§3 above) and picks the flavour automatically.
- Defer the implementation — but design §1 and §3 with the seam in mind.

## 11. WordPress-relevant footguns to pre-empt (§9)

| # | Footgun | Section above |
|---|---|---|
| F6 | Ambiguous `wp-settings.php` location | §3 |
| F7 | MariaDB 11.x collation in imported dump | §5 |
| F9 | `wp-config.php` clobbered on regen | §1 |
| F16 | Imported prod DB → redirect loop | §5 |

Each has a mitigation embedded in the plan above. The footguns table from
`LEARNINGS.md` should stay pinned during implementation.

---

## Phasing

Mapped to the Phase-1/Phase-2 buckets in `LEARNINGS.md` §10, narrowed to
WordPress concerns. The hooks engine is already shipped, so the
"runHooks" calls assumed below are no-cost.

### Phase 1 (next 1–2 milestones)

1. **`#locorum-generated` signature pattern** — adopt before §1, used
   everywhere we generate WP files.
2. **`wp-config.php` two-file split** (§1).
3. **Dynamic URL via env vars** (§2).
4. **WP-CLI helpers extracted** (§4).
5. **WordPress detection two-deep with fail-on-ambiguity** (§3).
6. **DB import with stripping regexes + auto search-replace** (§5).
7. **Auto-snapshot on PreDelete** (§6 #4) — touches WP only insofar as
   the snapshot covers the WP DB; mostly an `internal/sites` change.

### Phase 2 (3–6 months)

8. **Multisite first-class** (§7) — needs router wildcard support and
   mkcert wildcard certs to land first.
9. **HTTPS-via-XFP handler** (§8) — trivial; ship alongside §1 if router
   already sets the header.
10. **WP_DEBUG defaults + debug-log tail panel** (§9).

### Phase 3 (6–12+ months)

11. **Bedrock flavour** (§10).
12. **Hosting-provider DB pull** — out of scope here; tracked in
    `LEARNINGS.md` §10 #24.

## File-creation summary

| New file | Purpose | Section |
|---|---|---|
| `config/wordpress/wp-config.tmpl.php` | Embedded `wp-config.php` template (signed) | §1 |
| `config/wordpress/wp-config-locorum.tmpl.php` | Embedded freely-regen template | §1, §2, §7, §8, §9 |
| `internal/sites/wpconfig.go` | Render + write the two templates | §1 |
| `internal/sites/wpcli.go` | WP-CLI helper functions | §4 |
| `internal/sites/import_filters.go` | DB-dump stripping regexes | §5 |
| `internal/sites/sitesteps/wpconfig_step.go` | `WriteWPConfigStep` | §1 |
| `internal/sites/sitesteps/dbimport_steps.go` | Plan steps for `ImportDB` | §5 |
| `internal/ui/dbimport.go` | DB import wizard panel | §5 |
| `internal/ui/debuglog.go` | `wp-content/debug.log` tail tab | §9 |
| `internal/storage/migrations/20260501000000_add_wp_salts.{up,down}.sql` | `Salts` column on sites | §1 |

## Touched files

| File | Change | Section |
|---|---|---|
| `internal/types/types.go` | `Site.Salts string` | §1 |
| `internal/storage/storage.go` | column lists in CRUD | §1 |
| `internal/docker/specs.go` (or PHP spec builder) | `LOCORUM_*` env vars | §2 |
| `internal/sites/wordpress.go` | `detectWordPress`, lift `wp core install` | §3, §6 |
| `internal/sites/sites.go` | `ImportDB` + integration into Start/Delete plans | §5, §6 |
| `internal/router/traefik/` | Wildcard host support | §7 |
| `internal/tls/` (mkcert) | Wildcard cert SAN | §7 |
| `config/nginx/site.conf.tmpl` | Multisite rewrites guarded | §7 |
| `internal/ui/sitedetail.go` | Wire debug-log tab and import wizard entry | §5, §9 |
