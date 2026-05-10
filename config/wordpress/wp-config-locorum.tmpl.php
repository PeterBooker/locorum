<?php
// #locorum-generated — Locorum rewrites this file on every site start.
// Edits here will be lost. Put hand-written tweaks in wp-config.php
// (above the require_once line) where the !defined() guards below let
// them win.

// ── Reverse-proxy HTTPS detection ───────────────────────────────────────
// Traefik terminates TLS in the global router and forwards plain HTTP to
// the per-site web container. WP core honours X-Forwarded-Proto when
// behind a proxy, but many third-party plugins inspect $_SERVER['HTTPS']
// directly — so we set it here for compatibility.
if ( ! empty( $_SERVER['HTTP_X_FORWARDED_PROTO'] ) && 'https' === $_SERVER['HTTP_X_FORWARDED_PROTO'] ) {
	$_SERVER['HTTPS'] = 'on';
}
if ( ! empty( $_SERVER['HTTP_X_FORWARDED_HOST'] ) && empty( $_SERVER['HTTP_HOST'] ) ) {
	$_SERVER['HTTP_HOST'] = $_SERVER['HTTP_X_FORWARDED_HOST'];
}

// ── Database credentials ────────────────────────────────────────────────
if ( ! defined( 'DB_NAME' ) )     define( 'DB_NAME',     'wordpress' );
if ( ! defined( 'DB_USER' ) )     define( 'DB_USER',     'wordpress' );
if ( ! defined( 'DB_PASSWORD' ) ) define( 'DB_PASSWORD', getenv( 'MYSQL_PASSWORD' ) ?: '{{ phpEscape .DBPassword }}' );
if ( ! defined( 'DB_HOST' ) )     define( 'DB_HOST',     'database' );

// ── URLs ────────────────────────────────────────────────────────────────
// Baked in by Locorum at site-start time (internal/sites/wpconfig.go,
// computeWPURLs). PHP-FPM's default clear_env=yes strips Docker-set env
// vars before scripts run, so getenv('LOCORUM_PRIMARY_URL') would silently
// return false and any fallback would replace the real domain. Locorum
// regenerates this file on every start anyway; baking the resolved URL in
// is robust regardless of php-fpm pool config.
//
// LAN access (ACCESS.md): Locorum supports reaching the site via two
// hostnames simultaneously — the primary `*.localhost` form on the host
// machine, and a `<slug>.<dashed-ipv4>.<lan-domain>` form (default
// sslip.io) on phones and tablets on the same Wi-Fi. The constants
// below adapt WP_HOME / WP_SITEURL to whichever hostname served the
// request so generated links, emails, and redirects point back to the
// hostname the visitor is using.
//
// The host is matched against a strict whitelist BEFORE it becomes
// part of any URL — without this check, an attacker on the LAN could
// craft a request with a forged `Host:` header and have WordPress
// embed their hostname in a password-reset email. The whitelist is:
//   1. exact match against the primary domain (case-insensitive);
//   2. regex match against the per-slug LAN suffix (empty when LAN
//      access is disabled for this site, or unsupported for the site
//      type — currently subdomain multisite).
// Anything else falls back to the baked primary URL — a malformed
// `Host:` cannot redirect users off-platform.
$locorum_primary_host = '{{ phpEscape .PrimaryHost }}';
$locorum_lan_regex    = '{{ phpEscape .LANHostRegex }}';
$locorum_request_host = isset( $_SERVER['HTTP_HOST'] ) ? strtolower( (string) $_SERVER['HTTP_HOST'] ) : '';
$locorum_proto        = ( ! empty( $_SERVER['HTTPS'] ) && 'off' !== $_SERVER['HTTPS'] ) ? 'https' : 'http';
$locorum_host_allowed = ( $locorum_request_host === $locorum_primary_host )
	|| ( '' !== $locorum_lan_regex && 1 === preg_match( $locorum_lan_regex, $locorum_request_host ) );
if ( $locorum_host_allowed ) {
	$locorum_home = $locorum_proto . '://' . $locorum_request_host;
} else {
	$locorum_home = '{{ phpEscape .WPHome }}';
}
if ( ! defined( 'WP_HOME' ) )    define( 'WP_HOME',    $locorum_home );
if ( ! defined( 'WP_SITEURL' ) ) define( 'WP_SITEURL', $locorum_home . '{{ phpEscape .DocrootSuffix }}' );
unset( $locorum_primary_host, $locorum_lan_regex, $locorum_request_host, $locorum_proto, $locorum_host_allowed, $locorum_home );

// ── Debug ───────────────────────────────────────────────────────────────
if ( ! defined( 'WP_DEBUG' ) )         define( 'WP_DEBUG',         true );
if ( ! defined( 'WP_DEBUG_LOG' ) )     define( 'WP_DEBUG_LOG',     '/var/www/html/wp-content/debug.log' );
if ( ! defined( 'WP_DEBUG_DISPLAY' ) ) define( 'WP_DEBUG_DISPLAY', false );
if ( ! defined( 'SCRIPT_DEBUG' ) )     define( 'SCRIPT_DEBUG',     true );
if ( ! defined( 'WP_DISABLE_FATAL_ERROR_HANDLER' ) ) define( 'WP_DISABLE_FATAL_ERROR_HANDLER', true );

// ── Filesystem method ───────────────────────────────────────────────────
// 'direct' skips the FTP-credentials prompt for plugin/theme updates in
// local dev. The PHP user owns the bind-mounted files (see ChownStep), so
// direct writes succeed.
if ( ! defined( 'FS_METHOD' ) ) define( 'FS_METHOD', 'direct' );

// ── Disable auto-updates ────────────────────────────────────────────────
// Local dev sites should not silently upgrade their core. Use the GUI's
// version editor or wp-cli explicitly.
if ( ! defined( 'AUTOMATIC_UPDATER_DISABLED' ) ) define( 'AUTOMATIC_UPDATER_DISABLED', true );
if ( ! defined( 'WP_AUTO_UPDATE_CORE' ) )         define( 'WP_AUTO_UPDATE_CORE',         false );

{{- if .Multisite }}

// ── Multisite ───────────────────────────────────────────────────────────
// {{ .Multisite }} install. Wildcard hostname routing is configured by
// Locorum's router layer; you should not need to edit any of these
// constants by hand.
if ( ! defined( 'WP_ALLOW_MULTISITE' ) )    define( 'WP_ALLOW_MULTISITE',    true );
if ( ! defined( 'MULTISITE' ) )             define( 'MULTISITE',             true );
if ( ! defined( 'SUBDOMAIN_INSTALL' ) )     define( 'SUBDOMAIN_INSTALL',     {{ if eq .Multisite "subdomain" }}true{{ else }}false{{ end }} );
if ( ! defined( 'DOMAIN_CURRENT_SITE' ) )   define( 'DOMAIN_CURRENT_SITE',   '{{ phpEscape .Domain }}' );
if ( ! defined( 'PATH_CURRENT_SITE' ) )     define( 'PATH_CURRENT_SITE',     '/' );
if ( ! defined( 'SITE_ID_CURRENT_SITE' ) )  define( 'SITE_ID_CURRENT_SITE',  1 );
if ( ! defined( 'BLOG_ID_CURRENT_SITE' ) )  define( 'BLOG_ID_CURRENT_SITE',  1 );
{{- end }}
