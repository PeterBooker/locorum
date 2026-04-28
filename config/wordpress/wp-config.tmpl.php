<?php
// #locorum-generated — DO NOT remove this line if you want Locorum to keep
// this file in sync with the GUI. Removing the signature opts out of all
// future regeneration; Locorum will then never overwrite this file.
//
// Database credentials, WP_HOME, WP_DEBUG, and other Locorum-managed
// settings live in the included wp-config-locorum.php — that file is
// rewritten on every site start and should NOT be edited by hand.

// ── Locorum-managed include ─────────────────────────────────────────────
// Locorum writes wp-config-locorum.php with DB credentials, URLs, debug
// flags, and (when enabled) multisite constants. Each define() inside that
// file is guarded by !defined(...) so a hard-coded value below silently
// wins — exactly what you want when temporarily overriding.
if ( file_exists( __DIR__ . '/wp-config-locorum.php' ) ) {
	require_once __DIR__ . '/wp-config-locorum.php';
}

// ── WordPress secret keys ───────────────────────────────────────────────
// Generated once at site creation by Locorum. The salts are persisted in
// Locorum's database so this file regenerates byte-identically on every
// start (idempotent writes; see internal/sites/wpconfig.go).
{{- range $name, $value := .Salts }}
define( '{{ $name }}', '{{ phpEscape $value }}' );
{{- end }}

// ── Database table prefix ───────────────────────────────────────────────
$table_prefix = 'wp_';

// ── Charset / collate ───────────────────────────────────────────────────
// Set here (not in wp-config-locorum.php) because some plugins read these
// constants before wp-settings.php runs and expect them in wp-config.php.
if ( ! defined( 'DB_CHARSET' ) ) {
	define( 'DB_CHARSET', 'utf8mb4' );
}
if ( ! defined( 'DB_COLLATE' ) ) {
	define( 'DB_COLLATE', '' );
}

// ── ABSPATH ─────────────────────────────────────────────────────────────
if ( ! defined( 'ABSPATH' ) ) {
	define( 'ABSPATH', __DIR__ . '/' );
}

require_once ABSPATH . 'wp-settings.php';
