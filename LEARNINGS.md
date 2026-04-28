# Lessons from DDEV

A deep analysis of [DDEV](https://github.com/ddev/ddev) (`/home/peter/projects/ddev`)
to identify patterns, design decisions, and footguns Locorum should learn from.
DDEV solves the same problem space (local dev environments via Docker) and has
been battle-tested for ~10 years across macOS, Linux, Windows, and WSL2. We are
not copying DDEV — we are WordPress-only and GUI-first. But every category of
problem DDEV has solved, we will eventually hit.

For each finding: what DDEV does, where it lives in their tree, why it's smart,
and a verdict — **Adopt** (do this), **Adapt** (the pattern is right but the
form must change for a GUI/WP-only tool), or **Skip** (out of scope or
dis-economic for our scale).

---

## 1. Container Orchestration

### 1.1 Compose vs raw SDK

DDEV does not call the Docker Engine API directly. It generates a
docker-compose YAML per project (`pkg/ddevapp/app_compose_template.yaml`,
`pkg/ddevapp/router_compose_template.yaml`) and shells out to
`docker compose` (`pkg/dockerutil/docker_compose.go`). The `compose-spec/compose-go/v2`
library is used for parsing and merging YAML, not for orchestration.

**Why**: Compose handles network creation order, volume init, healthcheck
coordination, and inter-service DNS — years of edge cases the Compose team
already debugged. DDEV inherits all those fixes for free. Notably,
`pkg/ddevapp/ddevapp.go:1432-1486` has retry logic for the BuildKit "parent
snapshot does not exist" race (moby/buildkit#6521) that DDEV had to work
around when Docker 29+ landed.

**Verdict — Adapt**. Locorum already uses the SDK and is committed to it for
fine-grained GUI feedback. We accept the cost of re-implementing some of
Compose's startup-ordering logic. Mitigations:
- Run a startup-ordering test matrix against several Docker providers (Desktop,
  Colima, OrbStack, Podman, native Linux).
- Maintain an explicit retry loop for known-transient Docker errors. Start
  with: BuildKit snapshot race, "network already exists", and "container name
  in use" errors after a crashed previous start.
- Keep the option to swap to compose later if SDK direct-call cost outgrows
  the GUI integration benefits.

### 1.2 Container naming and labels

DDEV uses three labels on every container, network, and volume
(`pkg/dockerutil/labels.go`):

| Label | Purpose |
|---|---|
| `com.ddev.platform=ddev` | Marks DDEV-owned resources for global cleanup |
| `com.ddev.site-name=<project>` | Filters by project (empty for globals) |
| `com.ddev.webtag=<version>` | Image-version stamp for future pruning |

Container names are deterministic: `ddev-{site}-{service}` (e.g.
`ddev-myproject-web`). Labels — not names — are the source of truth for
discovery. `pkg/ddevapp/poweroff.go` uses label queries to find every
DDEV-owned resource for nuking.

**Verdict — Adopt**. Mirror this exactly with `com.locorum.platform`,
`com.locorum.site-name`, `com.locorum.version`. Names are for humans (logs,
`docker ps`); labels are for our code. Refactor `internal/docker/container.go`
to filter by label, not name prefix.

### 1.3 Two-tier networking

A global `ddev_default` network for the router and any cross-project comms,
plus per-project networks for isolation (`pkg/dockerutil/networks.go`). DDEV
also actively de-duplicates networks (`RemoveNetworkDuplicates`,
`networks.go:132-158`) because Docker daemon crashes can leave stale networks
that block re-creation.

**Verdict — Adopt**. Locorum already does the two-tier pattern — extend it
with the de-duplication safety net. Add a `ReconcileNetworks()` step to
`app.Initialize()` that removes orphans before creating new ones.

### 1.4 Pre-startup ownership fixup

Before starting service containers, DDEV runs a one-shot privileged container
that `chown`s the database volume and shared cache to the project UID/GID
(`ddevapp.go:1719-1760`). This avoids running services as root or doing
permissions-fixup inside long-running containers.

**Verdict — Adopt**. Pattern matches what we already do with PHP UID/GID
fallback in `main.go`. Generalise it: every site-start should run a one-shot
`alpine chown` against the DB volume and the user's site directory. This will
also fix the WordPress `wp-content/uploads` permissions issue that bites every
local-dev tool eventually.

### 1.5 Healthchecks and readiness polling

DDEV defines healthchecks per service in compose
(`app_compose_template.yaml:80-88`) — typically a `/healthcheck.sh` inside the
container, polled every 1s with up to 70 retries. After `docker compose up`,
DDEV polls the container state every 500ms via `ContainerWait`
(`pkg/dockerutil/containers.go:270-335`) and **dumps the full container log
when a wait times out**. This is critical: silent timeouts leave users staring
at "starting…" with no way to debug.

**Verdict — Adopt**. We currently `state.SetSiteToggling(false)` after
`StartSite()` returns, but we don't verify the containers reached a healthy
state. Add a `WaitForReady(siteID, timeout)` that polls health endpoints and,
on timeout, attaches the last 50 lines of each service log to the error toast.
This single change will eliminate a whole class of "I clicked Start and
nothing happened" bug reports.

### 1.6 Lifecycle ordering and rollback

DDEV's `Start()` (`ddevapp.go:1489-2100`) is a long sequence: pre-start hooks
→ idempotent compose-YAML write → image pull → volume create → ownership
fixup → SSH agent → `compose up` → `ContainerWait` → post-start hooks. Each
step is logged and partial failures stop the chain. `Stop()` is mirror-image
plus optional snapshot-before-delete.

The "idempotent compose YAML write" is subtle: if the rendered YAML is
byte-identical to what's on disk, DDEV skips writing it, which prevents
unnecessary container rebuilds.

**Verdict — Adapt**. We don't have user-defined hooks yet (see §5.4), but the
ordering discipline applies now: every site-start in `internal/sites/sites.go`
should have explicit, ordered, individually-loggable steps. The idempotent
nginx-config write is something we should add today.

### 1.7 Resource cleanup separation

DDEV draws a sharp line between "session cleanup" and "data cleanup":
- `ddev stop` → containers and per-project network removed; volumes and
  snapshots preserved.
- `ddev poweroff` → all `locorum-*` containers and networks; volumes
  preserved.
- `ddev delete --omit-snapshot` → still preserves snapshots.
- `ddev delete --all-volumes` → only this destroys DB data.

**Verdict — Adopt**. This is exactly our intent already (per CLAUDE.md
`Volumes are kept by design`). Make sure the GUI delete-confirmation modal
reflects this distinction with three radio choices: "stop only" / "delete site
(keep DB volume)" / "delete site and all data". Default to the middle option.

### 1.8 Centralised image versions

Every container image tag lives in `pkg/versionconstants/versionconstants.go`
as a Go constant (`WebTag`, `DBTag`, `TraefikRouterTag`, etc.). Compose
templates reference them as variables. Upgrades = bump constants in one file.

**Verdict — Adopt**. Move our hardcoded image references (currently spread
across `internal/docker/*`) into `internal/version/images.go` (or extend
`internal/version`). This makes "test against MySQL 8.4" a one-line change and
documents the supported matrix.

---

## 2. Router and TLS

### 2.1 Traefik over nginx

DDEV's global router is Traefik v3, not nginx
(`containers/ddev-traefik-router/`, `pkg/ddevapp/router.go`). Configuration
is **file-based with fsnotify watching**, not Docker-socket-based. When a new
project starts, DDEV writes a new YAML to the router's mounted config
directory (`~/.ddev/traefik/config/`); Traefik notices via inotify and
reloads — typically <1 second, no connection drops.

By contrast, our current nginx approach requires `nginx -s reload` which
briefly drops in-flight requests.

**Verdict — Adapt or Adopt — strong recommendation**. Traefik would simplify
several pain points:
- Hot-reload without dropping connections.
- Native ACME/Let's Encrypt support if we ever want real-domain testing.
- Built-in dashboard / API at `:8080` for diagnostics.
- Cleaner wildcard handling for WP multisite via `HostRegexp`.

The migration cost is real (template rewrites, learning Traefik's config
shape), but Locorum is early enough that it's cheaper to migrate now than
later. **Recommend a focused spike**: replace `locorum-global-webserver`
(nginx) with `locorum-global-router` (Traefik) and keep per-site nginx as the
in-container web server. This is exactly DDEV's split.

### 2.2 mkcert for local CA

DDEV does not bake self-signed certs into the router image. It detects
`mkcert` on the host (`pkg/globalconfig/global_config.go:885-901`,
`GetCAROOT`) and:
1. Generates a global default cert covering common hosts.
2. Generates per-project certs for primary host + `additional_hostnames` +
   `additional_fqdns` (`pkg/ddevapp/traefik.go:499-524`).
3. Marks generated files with a `#ddev-generated` signature comment so the
   user can opt out by removing the comment, and DDEV will then leave the
   file alone forever.

The user runs `mkcert -install` once; their browser shows green padlock for
every site forever after. mkcert handles macOS Keychain, Windows Cert Store,
and Linux NSS without DDEV touching elevation.

**Verdict — Adopt**. Right now we don't have a coherent TLS story. mkcert is
the right answer. Wrap detection + CA path discovery in
`internal/tls/mkcert.go`. If mkcert is missing, surface a clear modal: "For
HTTPS to work without browser warnings, install mkcert. [Show me how]" with
OS-specific instructions. Don't try to bundle it (licensing + cross-platform
distribution pain); just detect and guide.

### 2.3 The `#ddev-generated` signature pattern

Used everywhere DDEV writes a file the user might want to override:
`wp-config.php`, `wp-config-ddev.php`, generated certs, nginx configs. If the
signature is present, DDEV regenerates freely. If the signature was removed
(or never present, e.g. a user-imported `wp-config.php`), DDEV refuses to
overwrite and prints instructions for manual integration.

**Verdict — Adopt — high priority**. This is a tiny pattern with massive UX
payoff. Locorum should adopt `#locorum-generated` as a marker on every
generated file. It cleanly separates "config Locorum manages" from "config the
user owns" with zero schema or DB tracking.

### 2.4 Hostname management as a separate elevated binary

DDEV ships `cmd/ddev-hostname` as a separate binary
(`pkg/ddevapp/hostname_mgt.go`). Why separate?
- Elevation logic (sudo on Unix, runas/UAC on Windows) is platform-specific
  and lives in *one* place, not scattered.
- The main DDEV binary never needs root.
- On WSL2, DDEV invokes the *Windows* `ddev-hostname.exe` to edit the Windows
  hosts file (which both Windows and WSL share).

DDEV also tries DNS first: if the hostname already resolves to a local IP
(via `*.ddev.site` or a custom DNS server), it skips `/etc/hosts` entirely.

**Verdict — Adopt when needed**. Locorum doesn't manipulate `/etc/hosts`
today (we rely on `*.localhost` resolving to 127.0.0.1 by default on most
platforms). When we add custom-TLD support (e.g. `*.test`, `*.locorum.site`),
ship `cmd/locorum-hostname` as a separate elevated binary. Don't try to
elevate from the GUI process.

### 2.5 Port allocation and conflict fallback

DDEV probes ports 80/443 with `netutil.IsPortActive` before starting
(`pkg/ddevapp/router.go:604-650`). On conflict, it allocates an ephemeral
port from 33000-35000 and warns the user (`AllocateAvailablePortForRouter`,
`router.go:652-735`). Importantly: if *all* ephemeral ports report as busy,
DDEV assumes overzealous security software is blocking probes and proceeds
anyway with a warning.

**Verdict — Adopt**. We currently bind 80/443 hard. On a developer machine
with another local-dev tool already running (Local, MAMP, Valet), Locorum
fails opaquely. Add port-conflict detection at site-start and an ephemeral
fallback. Surface the resulting URL prominently in the UI.

### 2.6 Service routing via env vars

Any container that joins the global network and exports `VIRTUAL_HOST`,
`HTTP_EXPOSE`, and `HTTPS_EXPOSE` env vars gets auto-routed by DDEV's
`detectAppRouting()` (`traefik.go:29-78`). This is how Mailpit, Adminer, and
xhgui show up at `mail.ddev.site`, `db.ddev.site`, `xhgui.ddev.site` without
special-case code in DDEV.

**Verdict — Adopt — elegant**. Generalise our current hard-coded
`mail.localhost`/`db.localhost`/`adminer` routing into a "service container
declares its hostnames via labels, router picks them up" pattern. Future
services (e.g. `phpmyadmin`, `redis-commander`, `solr`) become drop-in.

### 2.7 Healthcheck-validated routing

DDEV's router container has a healthcheck script
(`containers/ddev-traefik-router/traefik_healthcheck.sh`) that hits Traefik's
own API at `:10999/api/http/routers` and validates expected vs actual route
counts. A config push that produces zero routers (broken YAML) fails the
healthcheck and surfaces the error.

**Verdict — Adopt**. After every nginx-config regeneration, hit
`http://locorum-global-webserver/healthz` (or whatever endpoint we define) and
verify the new site is reachable before declaring success. Today our reload
returns success on any nginx exit-0, even when the config is silently
ignoring a malformed server block.

---

## 3. WordPress-Specific Patterns

This section is the most directly applicable: Locorum is WP-only.

### 3.1 Two-file config split: `wp-config.php` + `wp-config-ddev.php`

In `pkg/ddevapp/wordpress/wp-config.php` (template) and
`pkg/ddevapp/wordpress.go:92-143`:

- **`wp-config.php`** (generated once with `#ddev-generated`): salts, charset,
  collation, conditional `require` of `wp-config-ddev.php`. Safe to overwrite
  if signature present; otherwise DDEV prints a snippet for the user to paste.
- **`wp-config-ddev.php`** (regenerated freely): DB credentials, `WP_HOME`,
  `WP_SITEURL`, `WP_DEBUG`. Always managed by DDEV.

The include guard in `wp-config-ddev.php` is `!defined('DB_USER')`, so if the
user hard-codes credentials in `wp-config.php`, DDEV's defaults silently lose
the race — which is what the user wanted.

**Verdict — Adopt — wholesale**. This is the single most important pattern
in the WordPress section. Replace any future temptation to write a single
`wp-config.php` with this split. Implement in `internal/sites/wordpress.go`.

### 3.2 Dynamic URL via env var

```php
defined('WP_HOME') || define('WP_HOME', getenv('LOCORUM_PRIMARY_URL') ?: 'http://localhost');
defined('WP_SITEURL') || define('WP_SITEURL', WP_HOME . '/' . /* path calc */);
```

(adapted from `pkg/ddevapp/wordpress/wp-config-ddev.php:25-39`)

The URL is resolved at PHP request time from an env var Locorum controls. This
means changing the site's primary domain in the GUI doesn't require rewriting
any PHP files — just recreate the container with new env. WP_SITEURL is
*calculated* from ABSPATH so subdir installs (`/wordpress/`) just work.

**Verdict — Adopt**. Locorum currently doesn't auto-rename URLs. This pattern
gives us URL changes for free. Set `LOCORUM_PRIMARY_URL`, `LOCORUM_DOCROOT`,
`LOCORUM_APPROOT` env vars on the PHP container.

### 3.3 WordPress detection: search for `wp-settings.php`, two levels deep

DDEV detects WP by looking for `wp-settings.php` in the docroot or one level
deeper (`wordpress.go:223-239`). If found in *multiple* nested dirs, it
**fails loudly** rather than guessing — ambiguity is a bug, not a feature.

**Verdict — Adopt**. Same logic for site import / "convert this directory to
a Locorum site" flows. Reject ambiguity explicitly.

### 3.4 WP-CLI as a pre-installed container tool

DDEV pre-installs wp-cli in the web container image (`ddev-php-base`) and
exposes it via `ddev exec wp <cmd>`. No special wrapper, no custom command —
just exec. The DDEV config template includes a *commented* example hook
(`wordpress.go:54-62`) suggesting `post-start: exec: wp cli version`.

**Verdict — Adopt**. Pre-install wp-cli in our PHP container. Add a
`internal/sites/wpcli.go` helper that wraps `docker exec` for the typical
operations (install, plugin install, search-replace, option update).
`internal/ui/wpcli.go` already exists — wire it through.

### 3.5 No automatic search-replace on DB import — the gap to fill

DDEV does **not** auto-rewrite URLs when you `ddev import-db`. It defines a
`PostImportDBAction` hook for each CMS, but the WordPress one is `nil`
(`apptypes.go:274-281`). Users importing a production DB get redirect loops
until they manually run `wp search-replace`.

**Verdict — Adopt the hook structure, fix the gap they left**. After
`import-db`, automatically run `wp search-replace 'https://prod.example.com'
'https://local.example.locorum.test'` for the common URL forms. Also offer a
"re-link URLs" button in the UI for ad-hoc cases.

This is a place where Locorum can genuinely beat DDEV for WordPress users.

### 3.6 CMS-specific defaults via `appTypeMatrix`

`pkg/ddevapp/apptypes.go` registers each CMS with a function map
(`settingsCreator`, `appTypeDetect`, `postImportDBAction`, `postStartAction`,
`importFilesAction`, `configOverrideAction`, `uploadDirs`, ...). Adding a new
CMS = registering one entry. PHP version, DB, webserver defaults are set per
CMS in `configOverrideAction` (e.g. Drupal 6 forces PHP 5.6).

**Verdict — Adapt for WP-only**. We don't need the full matrix, but we
*should* define the same set of "lifecycle hook points" for WordPress:
- `PostStart` — runs `wp core install` if no DB content.
- `PostImportDB` — runs the search-replace from §3.5.
- `PostImportFiles` — chmod uploads dir.
- `PreDelete` — auto-snapshot.

Encoding these as named functions on a single `WordPressManager` makes the
code easier to test and easier to extend (e.g. with Bedrock support — see §3.8).

### 3.7 Multisite is the user's job

DDEV does *not* auto-configure WordPress multisite constants. `additional_hostnames` is exposed
generically; WP multisite constants (`MULTISITE`, `SUBDOMAIN_INSTALL`,
`DOMAIN_CURRENT_SITE`, etc.) are documented as user-managed.

**Verdict — Adapt**. We should be slightly more opinionated since we're
WP-specific. Provide a multisite toggle in the new-site modal that:
- Sets `WP_ALLOW_MULTISITE`, `MULTISITE`, etc. in `wp-config-locorum.php`.
- Adds wildcard hostname (`*.{slug}.locorum.test`) to the routing layer.
- Generates a wildcard cert via mkcert.
- Configures the per-site nginx with the multisite rewrite rules.

Already partially in our schema (`add-multisite` migration exists). Wire it
through with the patterns above.

### 3.8 Bedrock is a separate project type

`pkg/ddevapp/wp_bedrock.go` registers `wp-bedrock` as its own type, not a
variant of `wordpress`. Detection: `config/application.php` exists. Differs in
docroot (`web` not auto-detect) and config-file management (writes `.env`, not
`wp-config.php`).

**Verdict — Adopt eventually, skip for v1**. Bedrock support is a clear
"phase 2" feature. Keep the lifecycle-hook abstraction (§3.6) flexible enough
that adding `wp-bedrock` later is registering one more set of hook
implementations, not a refactor.

### 3.9 HTTPS detection via reverse proxy

DDEV does **not** set `define('FORCE_SSL_ADMIN', true)` or similar; it
relies on the Traefik router setting `X-Forwarded-Proto: https` and on
WordPress respecting it. Some plugins/themes don't, which is a known WP
ecosystem issue, not DDEV's bug.

**Verdict — Adopt**. Make sure our nginx (or future Traefik) forwards the
header correctly. Document the `if ( !empty($_SERVER['HTTP_X_FORWARDED_PROTO']) && $_SERVER['HTTP_X_FORWARDED_PROTO'] === 'https' ) $_SERVER['HTTPS'] = 'on';` snippet for users who need it.

### 3.10 Default WP_DEBUG=true

`wp-config-ddev.php` sets `WP_DEBUG=true` by default but allows env
override. Sensible for local dev.

**Verdict — Adopt**. Also set `WP_DEBUG_LOG=true` and route the log to
`wp-content/debug.log`. Add a "WP Debug Log" tab to the site detail panel
that tails this file — instant debugging value for plugin developers.

---

## 4. Database and Snapshots

### 4.1 Binary backup, not SQL dump, for snapshots

Snapshots use `mariabackup` (MariaDB) / `xtrabackup` (MySQL) /
`pg_basebackup` (Postgres) streamed through zstd compression
(`pkg/ddevapp/snapshot.go`, `ddevapp.go:3103-3236`). Filename:
`{name}-{type}_{version}.zst` — type and version are encoded so restore can
reject incompatible engines.

Why binary: InnoDB page-level backup is consistent without long table locks,
and a 5GB DB takes seconds rather than minutes vs. mysqldump.

**Verdict — Adapt**. We're MySQL-only, so simpler. mysqldump
`--single-transaction --quick` piped through zstd is good enough for v1, but:
- Encode engine + version in the filename so future engine swaps don't
  silently corrupt restores.
- Stream to disk; never hold the dump in memory.
- Show progress in the UI (count rows or bytes streamed).

Move to physical backup (`mariabackup`) only when users complain about
snapshot speed on multi-GB sites.

### 4.2 Auto-snapshot before destructive operations

DDEV's `Stop(removeData, createSnapshot)` (ddevapp.go:3259-3289) takes a
snapshot named `{site}_remove_data_snapshot_{timestamp}` before destroying
data — and the snapshot survives the deletion. Users can `ddev snapshot
restore` even after `ddev delete`.

**Verdict — Adopt**. Before site deletion, always snapshot to
`~/.locorum/snapshots/{site}_pre_delete_{timestamp}.sql.zst`. Surface in the
delete-confirm modal: "A snapshot will be saved at … in case you want to
restore later." Massive trust-builder.

### 4.3 Streaming import with stripping regexes

`ddev import-db` (`ddevapp.go:880-1105`) supports `.sql`, `.sql.gz`,
`.sql.bz2`, `.sql.xz`, `.zip`, `.tar.gz`, `.tgz` via `pv` for progress and
**Perl regex substitution** to strip:
- `CREATE DATABASE` / `USE` (don't recreate the db)
- MariaDB 11.x `utf8mb4_uca1400_ai_ci` collation (incompatible across
  versions)
- `/*!999999...sandbox mode*/` comments

These are scars from years of "works for me but not in DDEV" bug reports.

**Verdict — Adopt — high priority for WordPress**. WordPress users routinely
import dumps from cPanel exports, WP Engine, Kinsta, etc. Each platform has
quirky export idioms. Implement the same Perl-style preprocessing in Go (or
shell out to `sed`/`perl` if simpler). Start with the three above; add more
as user reports come in. Document the list in source as
`internal/sites/import_filters.go`.

### 4.4 Import hooks: `pre-import-db` / `post-import-db`

Independent of WordPress URL rewriting (§3.5), DDEV exposes generic
`pre-import-db` and `post-import-db` hooks. Users wire in their own
`wp-cli`/`drush` calls.

**Verdict — Adopt as part of §5.4 hooks**. Even before the full hook system,
hard-code the WP search-replace as a `post-import-db` step.

### 4.5 Static credentials, exposed published port

DDEV uses `db/db/db` for user/password/database name across every project
(`ddevapp.go:274-290`). Security is a non-issue locally and the predictability
is a feature: docs are simpler, copy-paste works, support questions vanish.
The host-side connection port is randomly published and exposed in `ddev
describe`.

**Verdict — Adopt**. Already partly true for us. Standardise on
`db`/`db`/`db` for the trio. Surface the published port in the DB
Credentials panel with one-click copy.

### 4.6 Version-on-disk metadata for engine detection

DDEV writes the DB engine + version to a marker file inside the data volume
(`/var/tmp/mysql/db_mariadb_version.txt`) on initial creation
(`db.go:15-146`). On startup, it compares the marker against the configured
engine and refuses to start on mismatch with a clear error.

**Verdict — Adopt**. Cheap insurance. When changing a stopped site's MySQL
version in the version-editor UI, verify the volume marker is compatible
before allowing the change.

### 4.7 Dev-tuned MySQL config

`containers/ddev-dbserver/files/etc/my.cnf` is tuned for dev:
- `innodb_flush_log_at_trx_commit=2` (less durability, faster writes)
- `innodb_doublewrite=0` (no recovery redundancy)
- `innodb_buffer_pool_size=1024M`
- `skip-log-bin` (no replication overhead)
- charset `utf8mb4` (WP standard)

**Verdict — Adopt**. Bake these into our embedded `config/mysql/my.cnf`.
Allow override via `~/.locorum/config/mysql/conf.d/*.cnf` for power users.

### 4.8 Mailpit for transactional email

DDEV runs a Mailpit container (`mail.ddev.site`) that captures all outbound
SMTP. Locorum already does this — call this out as right.

**Verdict — Adopt (already done)**. Worth surfacing in the UI more
prominently; new WP users don't expect their plugins to send real email. A
"Mail Inbox" tab on the site detail page would be a low-effort, high-value
addition.

---

## 5. Configuration, Hooks, and Extensibility

### 5.1 YAML per project vs SQLite metadata

DDEV's source of truth is `.ddev/config.yaml` per project, schema-validated
via `pkg/ddevapp/schema.json` (JSON Schema draft-07). Locorum stores site
metadata in `~/.locorum/storage.db`. Both are valid; DDEV's approach
gets git-friendliness (`.ddev/config.yaml` lives in the project repo and
travels with the team), Locorum's approach gets centralised UI and queryable
state.

**Verdict — Hybrid**. Keep SQLite as the GUI's source of truth, but
**export** a `.locorum/config.yaml` per site (with `#locorum-generated`
header). Round-trip on read (if the YAML is newer than the DB row, prompt to
reconcile). This gives users:
- Git-tracked, team-shared site config.
- Reproducible site setup on a new machine.
- A familiar mental model for DDEV refugees.

The YAML is a *projection* of the DB row, not a competing source of truth.

### 5.2 Config merging with `config.*.yaml`

`config.local.yaml`, `config.prod.yaml`, etc. merge into `config.yaml` in
alphabetical order via `dario.cat/mergo` with a custom recursive merge
(`pkg/ddevapp/config.go:362-418`). Slices append by default; an
`override_config: true` flag in any override file makes slices replace
instead.

**Verdict — Skip for now**. Cute, but premature for a GUI-first tool with no
multi-environment story yet. Re-evaluate when users start asking for "I want
different settings on my work laptop vs home."

### 5.3 Global vs project config split

Global (`~/.ddev/global_config.yaml`) holds machine-wide defaults: router
ports, performance mode (mutagen on/off), CA root, instrumentation opt-in.
Project config inherits and may override most of them. Env vars override both
(`DDEV_TEST_*`).

**Verdict — Adopt**. Today, all our config is per-site. We need a
`global_settings` table (or `~/.locorum/config/global.yaml`) for things like:
- Default PHP / MySQL versions for new sites.
- Telemetry opt-in (see §7.3).
- Performance mode (mutagen on macOS).
- Custom mkcert path.
- Router ports.

The Settings panel becomes the GUI for this.

### 5.4 The hooks system — **IMPLEMENTED**

> **Status (2026-04-26):** shipped. `internal/hooks/` is the engine,
> `internal/storage/hooks.go` is the CRUD, `internal/ui/hookspanel.go` is
> the GUI. Plan: `HOOKS.md`. Full reference docs: `CLAUDE.md` § Hooks.

What landed (vs the original DDEV-inspired sketch below):

- **SQLite-backed**, not YAML. The `site_hooks` table is FK-cascaded on
  site delete and ordered per `(site, event)` by an explicit `position`
  column. YAML export remains a future workstream (§5.1).
- **Three task types**: `exec` (default service `php`, override via
  dropdown to `web`/`database`/`redis`), `exec-host` (`bash -c` on
  Linux/macOS/WSL, `cmd /C` on native Windows), `wp-cli` (sugar over
  `exec` in the `php` container). `composer` task type was skipped
  (decision H4): users can `exec: composer install`.
- **Active events**: `pre-/post-start`, `pre-/post-stop`,
  `pre-/post-delete`, `pre-/post-clone`, `pre-/post-versions-change`,
  `pre-/post-multisite`, `pre-/post-export`. Reserved names exist for the
  import / snapshot lifecycle but their firing sites are still TODO.
- **Pre-start exec is rejected at save AND run time** (decision H6 —
  defence-in-depth, going one better than DDEV's runtime-only check).
- **Variable interpolation is shell-time** (decision H7), matching DDEV.
  The per-task env bundle is `LOCORUM_*` (§3.4 of `HOOKS.md`).
- **Fail policy**: per-site flag preferred over global (`hooks.fail_on_error.<id>`,
  fallback `hooks.fail_on_error.global`). Default is warn — a failing hook
  logs and the lifecycle method continues.
- **Per-run logs**: `~/.locorum/hooks/runs/<slug>/<event>-<ts>.log`,
  swept at startup (30 days OR 50 per site). `LOCORUM_SKIP_HOOKS=1`
  short-circuits the runner.

The original DDEV reference (kept here for context):

### 5.5 Addons

`pkg/ddevapp/addons.go` defines an addon as a directory with:
- `install.yaml` manifest (name, project_files, global_files, pre/post-install
  actions, removal_actions, ddev_version_constraint, dependencies).
- Files installed into `.ddev/` (mostly extra docker-compose YAMLs).
- Bash or PHP install scripts run in the web container.

Addons are distributed as GitHub releases. Installed addons tracked in
`.ddev/addon-metadata/{name}/manifest.yaml`.

**Verdict — Adapt — phase 2**. The community ecosystem of DDEV addons (Solr,
Varnish, Elasticsearch, debug tools, etc.) is one of its competitive
strengths. Locorum should plan for the same eventually, with a difference:

- **Container addons** behave identically to DDEV: drop in extra services
  via metadata, route them via §2.6.
- **GUI addons** would let plugins extend the Gio UI itself — a riskier path,
  defer until we have a stable internal API.

Defer the full system; for now expose §2.6 as the "any container that
declares `VIRTUAL_HOST` joins the routing" pattern, which is the substrate
on which addons will eventually sit.

### 5.6 Custom commands

DDEV scans `.ddev/commands/{web,db,host}/{cmd}` and exposes them as
`ddev {cmd}` (`pkg/ddevapp/commands.go`). Bundled defaults are embedded via
`go:embed` from `dotddev_assets/` and copied to `~/.ddev/commands/` on
upgrade.

**Verdict — Skip in current form**. We don't have a CLI to extend.
Equivalent in our world: store user scripts in the DB, expose as buttons in a
"Site Actions" panel. Revisit when needed; not a v1 feature.

### 5.7 Asset embedding via `go:embed`

DDEV uses `go:embed` to bundle templates, default scripts, and reference
configs into the binary, then extracts them to `~/.ddev/` on first run.
`CheckCustomConfig()` (`assets.go:48-308`) compares disk content against
embedded reference and warns if the user modified bundled files.

**Verdict — Adopt**. We already use `go:embed` for nginx configs and
migrations. Extend the pattern: embed default site templates, example hooks,
the `wp-config.php` skeleton. Add a "diff against bundled default" check on
upgrade so users know which of their tweaks survive a reset.

### 5.8 Schema evolution discipline

DDEV keeps deprecated YAML field names alive (e.g. `upload_dir` singular →
`upload_dirs` plural) by reading both and warning on the deprecated one.

**Verdict — Adopt**. Our SQLite migrations handle schema evolution well, but
when we serialise to YAML (per §5.1), be deliberate about renames: support
old + new for at least one major version, warn on old, drop after.

---

## 6. Cross-Platform and Performance

### 6.1 WSL2 detection: env + /proc

DDEV checks `WSL_INTEROP` env var **and** `/proc/version` for `-microsoft`
(`pkg/nodeps/wsl.go`). Either alone is unreliable: env vars get cleared in
sub-shells and CI, `/proc/version` doesn't show `microsoft` on some custom
WSL kernels.

DDEV also checks `wslinfo --networking-mode` to detect NAT vs mirrored vs
virtioproxy networking, which materially changes how `localhost` works.

**Verdict — Adopt**. Our `internal/utils/utils.go` only checks
`WSL_DISTRO_NAME`. Add `/proc/version` fallback. Cache the result. Store the
networking mode if we can detect it; warn the user if the project lives under
`/mnt/c/...` (10x slower than native WSL FS).

### 6.2 Docker provider detection

DDEV calls `docker info` *once*, caches the result, and inspects
`OperatingSystem` and `Name` to identify Docker Desktop, Colima, OrbStack,
Rancher Desktop, Lima, Podman (`pkg/dockerutil/providers.go`). Each provider
has different defaults for mount speed, rootless behaviour, and platform
support. Mutagen is enabled or skipped accordingly.

**Verdict — Adopt — high priority**. We currently don't know what Docker
we're talking to. This means:
- We can't warn macOS Docker Desktop users that they need mutagen for usable
  performance.
- We can't tell Podman users their networking might behave differently.
- We re-call `docker info` on every operation.

Add `internal/docker/provider.go` with a cached provider type. Use it to
drive mutagen-or-not, performance warnings, and provider-specific quirks.

### 6.3 Mutagen on macOS Docker Desktop

DDEV's biggest cross-platform investment. Docker Desktop on macOS uses
VirtioFS for bind mounts, which is ~10-100x slower than native filesystem for
WordPress's many-small-files workload. Mutagen runs a sync daemon between the
host filesystem and a Docker named volume, achieving near-native speed.

DDEV (`pkg/ddevapp/mutagen.go`) handles installation, lifecycle (pause vs
terminate — pausing is ~1s to resume, terminating is ~3s to recreate),
config-hash detection (rebuilds sync if config changed), and recovery
(volume-signature labels detect drift).

**Verdict — Adopt — eventually critical**. macOS users without mutagen will
find Locorum unusable on real WordPress installs (>5,000 files). Roadmap:
- v0.x: detect macOS + Docker Desktop, warn user with a link to "install
  Colima or OrbStack instead."
- v1.x: optional mutagen integration with auto-install.
- v2.x: mutagen by default on macOS Docker Desktop with a status indicator
  in the UI.

### 6.4 Username sanitisation for containers

DDEV had a bug where usernames with diacritics ("André Kraus") or spaces
broke container creation. The fix
(`pkg/dockerutil/containers.go:91-130`) strips diacritics, lowercases,
replaces invalid chars, handles Windows `DOMAIN\user` format. On Windows it
just hardcodes `1000:1000` because Windows has no UID/GID.

**Verdict — Adopt**. Mirror the sanitisation in
`internal/utils/user.go`. Today our PHP UID/GID fallback is right for
Windows but we don't sanitise usernames that get baked into container env or
file ownership. Quiet bug waiting to bite an international user.

### 6.5 OS warnings system

`pkg/ddevapp/os_warnings_darwin.go` hard-fails on Apple Silicon if running
under Rosetta. Other OS-specific files contain platform-targeted warnings.
DDEV proactively warns users about:
- Project on `/mnt/c/...` (slow WSL filesystem).
- Missing mkcert.
- Low disk space (runs `df` in a container).
- Conflicting local-dev tools.

**Verdict — Adopt — translate to GUI**. A CLI tool dumps warnings to stderr;
a GUI tool needs a "System Health" panel:
- **Blocker** (modal, can't dismiss): Rosetta, no Docker, Docker not running.
- **Warning** (dismissible toast + persistent badge): slow FS, missing
  mkcert, low disk, port conflicts.
- **Info** (status bar): provider, mutagen status, disk %.

Run all checks at startup (in `app.Initialize`'s background goroutine).

### 6.6 Path conversion utilities

DDEV uses `filepath.ToSlash` everywhere for path normalisation. WSL paths get
converted via `wslpath`. Windows long-path limits (260 chars) are not
explicitly checked but DDEV defaults to short paths under `$HOME` to stay
safe.

**Verdict — Adopt**. We do some of this. Add an explicit length-check on
site creation: if `~/locorum/sites/{slug}/wp-content/plugins/...` would
plausibly exceed 200 chars on Windows, warn with the suggested fix
(`LongPathsEnabled` registry key).

### 6.7 Disk space monitoring

DDEV runs a containerised `df` to check Docker's disk usage and warns below
5GB free.

**Verdict — Adopt**. Run on startup, refresh every 5 minutes. Display in the
status bar. Warn modal at <5GB free.

---

## 7. CLI/UX Patterns Translated for the GUI

DDEV is CLI-first; we are GUI-first. The patterns translate.

### 7.1 Typed errors at the boundary, messages at the UI

`pkg/ddevapp/errors.go` defines typed errors (`type invalidConfigFile error`)
without user-facing wording. Wording lives in `cmd/ddev/cmd/*` where
context-appropriate messages with remediation steps are generated.

**Verdict — Adopt**. Do not write user-facing strings in
`internal/sites/*` or `internal/docker/*`. Surface typed errors; let
`internal/ui/*` decide how to render them with action buttons ("Retry",
"Open Docker", "View logs").

### 7.2 NO_COLOR, JSON output, verbosity

DDEV respects `NO_COLOR` env var (no-color.org standard), supports
`--json-output` for scripting, and `DDEV_DEBUG=true` for verbose execution
(`pkg/output/`).

**Verdict — Adopt for the future CLI companion**. We don't have a CLI yet.
When we add one (likely needed for CI / "sites as code" workflows), bake in
JSON output and structured logging from day one. Within the GUI, expose a
"Debug Mode" toggle in Settings that increases log verbosity in the in-app
log panel.

### 7.3 Telemetry done thoughtfully

DDEV's instrumentation
(`pkg/amplitude/`, `pkg/ddevapp/amplitude_project.go`):
- **Opt-in only**. Off until the user explicitly opts in.
- Hashed device ID via `machineid.ProtectedID()` (no raw UUID).
- Project IDs hashed (`hash("ddev" + project_name)`).
- Rich payload: PHP version, services running, addons installed, errors.
- **Never**: file paths, URLs, credentials, plugin/theme names, user code.
- Batched + delayed transmission (24h or 100 events). Network-aware.
- API key injected at build time, not committed.

**Verdict — Adopt — exactly this design**. Show an opt-in modal on first
launch (with privacy doc link). What to track:
- Lifecycle events (site created/started/stopped/deleted).
- Counts (sites currently configured, sites currently running).
- Error categories (Docker unreachable, port conflict, mkcert missing).
- Feature use (multisite enabled? mutagen enabled? wp-cli used?).

Skip by default. Let users opt in once they trust the tool.

### 7.4 Update checking with file-mtime throttling

DDEV checks GitHub releases at most every 4 hours
(`pkg/updatecheck/`). Throttle is a file-mtime check, persists across
sessions. If a new version exists, surfaced on next interactive command;
never auto-updates.

**Verdict — Adopt**. Background goroutine on app startup; cache result in
`~/.locorum/state/last_update_check`. Show a dismissible banner in the
sidebar when an update is available. Don't auto-update (signed-binary
distribution is enough friction).

### 7.5 `Describe()` as the canonical site-status function

DDEV's `Describe()` returns a `map[string]any` with ~30 fields: name, status,
URLs, DB info, PHP version, services, ports, Xdebug status, etc. Rendered as
table or JSON depending on `--json-output`.

**Verdict — Adopt**. Add `internal/sites/describe.go` returning a
strongly-typed `SiteDescription` that the UI renders. Keep it pure (no
side-effects) so it's safe to call in render loops.

### 7.6 `poweroff` as a panic button

`pkg/ddevapp/poweroff.go` removes every `locorum-*` container, network, and
the global router; preserves data volumes. Idempotent (errors-as-warnings).

**Verdict — Adopt**. Settings → "Reset Locorum infrastructure" button with
a confirm modal. Useful when Docker state gets corrupted (which happens —
nightly Docker Desktop updates, kernel panics, force-quits).

### 7.7 Multi-service log streaming

`ddev logs --follow --service web` streams Docker logs with timestamps.
`pkg/ddevapp/`'s log multiplexer prefixes each line with the service name.

**Verdict — Adopt — already partially done**. Our `internal/ui/logviewer.go`
exists. Make sure it: streams (no buffer-then-render), supports filter by
service, syntax-highlights error lines, exports to file, and survives site
restarts (re-attach to the new container automatically).

### 7.8 Custom commands and shell-into-container

`ddev ssh` allocates a TTY and execs the configured shell as the configured
user. DDEV uses Bash by default but falls back to `sh` for minimal images.

**Verdict — Adapt**. We can't embed a terminal in Gio cheaply. Provide a
"Shell" button per service that:
- Copies the `docker exec -it locorum-{site}-{service} bash` command to
  clipboard.
- Optionally launches the user's terminal emulator with the command (per the
  ML4W default `kitty` config — see §6.6 platform paths).

---

## 8. Testing and Release Engineering

### 8.1 Test against real Docker, no mocks

Integration tests in `pkg/ddevapp/ddevapp_test.go` start real containers and
assert against real HTTP responses for 8 CMS variants. `TestSites` array has
a tarball URL + DB dump + expected URI per CMS. Tests run sequentially
(`-p 1`) with a 6-hour timeout.

**Verdict — Adopt — high priority**. We have unit tests for storage and
nginx templating but nothing exercising real Docker. Build
`internal/sites/integration_test.go` that:
- Creates a site, asserts containers are running, hits the URL, asserts WP
  installer page renders.
- Stops the site, asserts containers stopped, volumes remain.
- Deletes the site, asserts containers gone, volumes gone (if requested).

Mark these `-tags=integration` so they only run in CI and on demand. Skip
parallel execution.

### 8.2 `testcommon` helper package

`pkg/testcommon/testcommon.go` centralises test fixtures and helpers:
`TestSite` struct, `GetCachedArchive` (download once, reuse), `EnsureLocalHTTPContent`
(retry with backoff), `CopyGlobalDdevDir` (test isolation),
`ClearDockerEnv` (unset 30+ Docker env vars between tests).

**Verdict — Adopt**. Build `internal/testutil/` with:
- `SetupTestSite(t)` — creates a temp site, registers `t.Cleanup`.
- `WaitForHTTP(url, timeout)` — exponential backoff to a 200 response.
- `FixtureWordPressArchive()` — cached download of latest WP.

### 8.3 Static analysis

`.golangci.yml` enables exactly 7 linters: `errcheck`, `govet`,
`ineffassign`, `modernize`, `revive`, `staticcheck`, `whitespace`. Plus
markdownlint for docs.

**Verdict — Adopt — minimal config**. Don't over-engineer linting. Adopt
DDEV's exact 7-linter set. Add gofmt enforcement in CI.

### 8.4 Manual changelog

`version-history.md` (29k of hand-written notes) lists user-visible changes
per release. Not auto-generated from commits — that produces noise; humans
write better changelogs.

**Verdict — Adopt**. Maintain `CHANGELOG.md` in Keep-a-Changelog format. One
section per release, user-focused ("Added Mailpit integration") not
commit-derived ("refactor smtp service").

### 8.5 Vendored dependencies

DDEV checks `vendor/` into git. Guarantees reproducible builds even during
GitHub outages, but adds ~500MB to the repo.

**Verdict — Skip**. Premature for our scale. Re-evaluate if we hit a
dependency-availability incident.

### 8.6 Release engineering

`.goreleaser.yml` (21KB!) handles cross-platform builds, code-signing
(macOS notarization, Windows signtool), Chocolatey packaging, GitHub release
creation. We already have `goreleaser` set up but unsigned.

**Verdict — Adopt incrementally**. v0.x: unsigned binaries with checksums
(current state, fine). v1.0: macOS notarization via Apple Developer (~$99/yr)
to remove "unidentified developer" warnings. Windows signing later (~$200/yr
cert). Linux: AppImage or Flatpak distribution.

### 8.7 Bats tests for docs examples

`docs/tests/` contains Bats tests that execute every CLI example from the
docs. Catches doc rot (commands renamed without docs updating).

**Verdict — Skip for now**. Our docs are minimal. Revisit when README grows.

### 8.8 GUI testing strategy

DDEV is CLI; this is uncharted for us. The pragmatic answer:
- Keep `internal/ui/` thin — it should *display* state and *invoke* business
  logic, not contain logic.
- Push all logic into `internal/sites/`, `internal/docker/`, `internal/storage/`
  where it's testable headlessly.
- Skip automated GUI testing for v0.x. Manual smoke test at release: create
  site → start → open in browser → install WP → stop → delete.
- Revisit when we hit Gio test tooling maturity (currently weak).

**Verdict — Adopt the discipline**. The rule "logic is testable, UI just
renders" is the single most important architectural choice we can make for
long-term test stability.

---

## 9. Battle-Tested Footguns

A consolidated list of bugs DDEV has clearly fixed over time. Each is
something we will eventually hit if we don't pre-empt it.

| # | Footgun | Source | Pre-emption |
|---|---|---|---|
| F1 | Username with diacritics breaks containers | §6.4 | Sanitise usernames everywhere |
| F2 | Config change mid-mutagen-sync corrupts volume | §6.3 | Hash config; reset volume on mismatch |
| F3 | `docker compose down` leaves orphan containers | §1.7 | Label-based cleanup loop after every stop |
| F4 | Stale Docker network blocks recreate after crash | §1.3 | De-dup networks on every start |
| F5 | BuildKit "parent snapshot" race on Docker 29+ | §1.1 | Retry logic for known-transient errors |
| F6 | Multiple `wp-settings.php` ambiguity | §3.3 | Fail loudly, never guess |
| F7 | Imported DB has incompatible MariaDB collation | §4.3 | Strip via Perl-style regex on import |
| F8 | DB version drift between volume and container | §4.6 | Marker file in volume, refuse mismatched start |
| F9 | User edits `wp-config.php`, next start clobbers | §3.1, §2.3 | `#locorum-generated` signature |
| F10 | Apple Silicon user installs amd64 binary | §6.5 | Rosetta detection + hard-fail |
| F11 | WSL project lives on `/mnt/c/...` (slow) | §6.1 | Detect on startup, warn |
| F12 | Long Windows path exceeds 260 chars | §6.6 | Pre-validate site-name + path length |
| F13 | nginx reload silently succeeds with bad config | §2.7 | Healthcheck-validate router after reload |
| F14 | Port 80/443 already used by other dev tool | §2.5 | Probe + ephemeral port fallback |
| F15 | mutagen daemon left over from previous DDEV install | §6.3 | Cleanup on poweroff |
| F16 | Imported prod DB → redirect loop in WP | §3.5 | Auto-search-replace on import |
| F17 | Wait-for-container times out with no logs | §1.5 | Dump container log on wait timeout |
| F18 | Snapshot restore on wrong DB engine corrupts data | §4.1 | Encode engine+version in filename |
| F19 | mkcert + Java env breaks `mkcert -install` | §2.2 | Document; not auto-installable |
| F20 | Telemetry payload leaks user URLs/paths | §7.3 | Hashed IDs only; no raw user data |

Print this and pin it to the planning wall.

---

## 10. Recommended Roadmap

A prioritised cut of the above for actual implementation. "Strong" =
significant user-visible win or footgun pre-emption. "Quiet" = invisible
correctness or code-quality improvement.

### Phase 1 — Adopt now (next 1-2 milestones)

1. **`#locorum-generated` signature pattern** (§2.3, §3.1) — Strong, low
   effort. Apply to every generated file.
2. **WordPress two-file config split** (§3.1) — Strong. Replace any
   single-file `wp-config.php` plan.
3. **Dynamic URL via env var** (§3.2) — Strong. Frees us from URL-rewrite
   pain forever.
4. **Auto-snapshot before destructive ops** (§4.2) — Strong trust-builder.
5. **Auto-search-replace on DB import** (§3.5) — Strong; clear DDEV gap to
   beat.
6. **Label-based container/network discovery** (§1.2) — Quiet. Refactor
   `internal/docker/`.
7. **Idempotent config writes** (§1.6) — Quiet, prevents needless rebuilds.
8. **WaitForReady with log-on-timeout** (§1.5) — Strong. Eliminates a class
   of bug reports.
9. **Centralised image versions** (§1.8) — Quiet, enables fast version-matrix
   testing.
10. **Docker provider detection** (§6.2) — Quiet now, enables Phase 2.

### Phase 2 — Adopt next (3-6 months)

11. ~~**Hooks system** (§5.4)~~ — **Shipped 2026-04-26.** See `HOOKS.md` for the plan, `CLAUDE.md` § Hooks for the reference.
12. **OS warnings system** (§6.5) — Strong, GUI translation of DDEV's
    proactive checks.
13. **mkcert integration** (§2.2) — Strong, removes browser-cert UX pain.
14. **Mutagen on macOS Docker Desktop** (§6.3) — Strong for macOS users.
15. **WP multisite first-class support** (§3.7) — Strong differentiator.
16. **YAML config export** (§5.1) — Strong. "Sites as code" / team sharing.
17. **Service routing via container labels** (§2.6) — Quiet, enables addons.
18. **Real-Docker integration tests** (§8.1) — Quiet, prevents regressions.
19. **`testutil` package** (§8.2) — Quiet, makes integration tests bearable
    to write.
20. **Username sanitisation** (§6.4) — Quiet footgun pre-emption.

### Phase 3 — Adopt eventually (6-12+ months)

21. **Traefik replacement of nginx as router** (§2.1) — Strong but expensive
    refactor. Window closes the longer we wait.
22. **Telemetry (opt-in)** (§7.3) — Strong product input source.
23. **Bedrock support** (§3.8).
24. **Hosting-provider DB pull (Kinsta/WP Engine/Pressable APIs)** (§3.5+)
    — Strong differentiator if they expose stable APIs.
25. **macOS notarization, Windows signing** (§8.6) — Strong UX, costs money.
26. **Addons system** (§5.5) — Strong ecosystem play, deep.
27. **CLI companion** (§7.2) — Lets us join CI workflows.

### Skip / decline

- **`config.*.yaml` merging** (§5.2) — Premature.
- **Vendored deps** (§8.5) — Premature.
- **Bats doc tests** (§8.7) — Premature.
- **Custom user commands as scripts** (§5.6) — Subsumed by hooks.

---

## Appendix — Where to look in DDEV

Quick map for future spelunking:

| Topic | Path |
|---|---|
| App lifecycle | `pkg/ddevapp/ddevapp.go` |
| Compose templates | `pkg/ddevapp/{app,router}_compose_template.yaml` |
| Docker wrappers | `pkg/dockerutil/` |
| Routing & TLS | `pkg/ddevapp/{router,traefik}.go`, `containers/ddev-traefik-router/` |
| WordPress | `pkg/ddevapp/wordpress.go`, `pkg/ddevapp/wp_bedrock.go`, `pkg/ddevapp/wordpress/` |
| CMS abstraction | `pkg/ddevapp/apptypes.go` |
| Snapshots | `pkg/ddevapp/snapshot.go` |
| DB import/export | `pkg/ddevapp/ddevapp.go` (Import/ExportDB), `cmd/ddev/cmd/import-db.go` |
| Config schema | `pkg/ddevapp/schema.json`, `pkg/ddevapp/config.go` |
| Hooks | `pkg/ddevapp/ddevapp.go` (ProcessHooks), `pkg/ddevapp/hooks_test.go` |
| Addons | `pkg/ddevapp/addons.go` |
| Mutagen | `pkg/ddevapp/mutagen.go` |
| Cross-platform helpers | `pkg/nodeps/`, `pkg/util/`, `pkg/fileutil/` |
| Telemetry | `pkg/amplitude/`, `pkg/ddevapp/amplitude_project.go` |
| Update check | `pkg/updatecheck/` |
| Test helpers | `pkg/testcommon/` |
| Release config | `.goreleaser.yml`, `Makefile`, `containers/containers_shared.mk` |
