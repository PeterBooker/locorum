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
if ( ! defined( 'WP_HOME' ) )    define( 'WP_HOME',    '{{ phpEscape .WPHome }}' );
if ( ! defined( 'WP_SITEURL' ) ) define( 'WP_SITEURL', '{{ phpEscape .WPSiteURL }}' );

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
