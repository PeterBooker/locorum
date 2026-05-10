package sites

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

// autoLoginPluginRelPath is the on-disk location of the persistent mu-
// plugin relative to the site's app root (FilesDir + PublicDir). It is
// installed once at site start and stays in place; the per-click write
// targets a separate token file (autoLoginTokenRelPath).
const autoLoginPluginRelPath = "wp-content/mu-plugins/locorum-autologin.php"

// autoLoginTokenRelPath is the per-click ephemeral one-time token. The
// mu-plugin reads it, validates against the URL's locorum_token query
// param, and deletes the file before authenticating so a duplicate
// request (browser prefetch, retry) cannot reuse it.
//
//nolint:gosec // G101: filename of a token file, not the token itself.
const autoLoginTokenRelPath = "wp-content/.locorum/login-token"

// autoLoginPluginBody is the persistent mu-plugin source. It carries
// the locorum-generated marker so genmark.WriteIfManaged respects a
// user-stripped signature (F9 — see invariant 16). The file is
// idempotent across starts and never deleted by Locorum itself.
//
// Behaviour:
//
//   - No-op unless the request has ?locorum_token=X.
//   - Reads the disk token from autoLoginTokenRelPath; absent / empty
//     means "no pending login," so the plugin stays silent and lets WP
//     handle the request normally (no surprise redirects to login).
//   - hash_equals + length pre-check defeat per-byte timing leaks even
//     though the threat model is single-user desktop.
//   - One-time use: deletes the disk token *before* authenticating, so
//     a concurrent duplicate request cannot reuse it.
//   - If admin user lookup fails, the plugin returns silently rather
//     than redirecting to admin_url() unauthenticated — the previous
//     design produced a confusing wp-login.php?reauth=1 loop on edge
//     cases (e.g. a click that landed before wp core install ran).
const autoLoginPluginBody = `<?php
// #locorum-generated — DO NOT remove this line if you want Locorum to keep
// this file in sync. Removing the signature opts out of all Locorum-
// managed updates to this file forever.
//
// Persistent auto-login helper. Reads an ephemeral token from
// wp-content/.locorum/login-token written by the GUI on each "View
// Admin" click.

if (!defined('ABSPATH')) { exit; }
if (!isset($_GET['locorum_token'])) { return; }

$tokenFile = WP_CONTENT_DIR . '/.locorum/login-token';
if (!is_readable($tokenFile)) { return; }

$disk = @file_get_contents($tokenFile);
if ($disk === false) { return; }
$disk = trim($disk);
if ($disk === '') { return; }

$urlToken = (string) $_GET['locorum_token'];
if (strlen($urlToken) !== strlen($disk)) { return; }
if (!hash_equals($disk, $urlToken)) { return; }

// One-time use: delete BEFORE authenticating so a concurrent duplicate
// request (browser prefetch, double-click) cannot reuse the same token.
@unlink($tokenFile);

add_action('init', function () {
    $user = get_user_by('login', 'admin');
    if (!$user) {
        $users = get_users(['role' => 'administrator', 'number' => 1]);
        $user = !empty($users) ? $users[0] : null;
    }
    if (!$user) {
        // No admin user yet (rare: WP install in flight). Stay silent
        // rather than redirecting unauthenticated — the user lands on
        // the URL they navigated to (likely the install screen) and
        // can self-recover. The old design's unconditional redirect
        // produced a confusing wp-login.php?reauth=1.
        return;
    }
    wp_set_current_user($user->ID);
    wp_set_auth_cookie($user->ID, true);
    wp_safe_redirect(admin_url());
    exit;
});
`

// installAutoLoginPlugin writes the persistent mu-plugin idempotently
// at the site's app root. genmark.WriteIfManaged guarantees:
//
//   - First write: the marker line is included verbatim from the body.
//   - Re-runs with byte-identical content: short-circuits, no IO.
//   - User has stripped the marker: write is skipped (the user has
//     opted out of Locorum-managed updates to this file forever).
//
// Errors other than ErrUserOwned propagate so a malformed permissions
// state surfaces during start rather than at click time.
func installAutoLoginPlugin(site *types.Site) error {
	if site == nil || site.FilesDir == "" {
		return errors.New("installAutoLoginPlugin: empty site")
	}
	pluginPath := filepath.Join(autoLoginAppRoot(site), filepath.FromSlash(autoLoginPluginRelPath))
	if err := utils.EnsureDir(filepath.Dir(pluginPath)); err != nil {
		return fmt.Errorf("creating mu-plugins dir: %w", err)
	}
	if err := genmark.WriteIfManaged(pluginPath, []byte(autoLoginPluginBody), 0o600); err != nil &&
		!errors.Is(err, genmark.ErrUserOwned) {
		return fmt.Errorf("writing auto-login mu-plugin: %w", err)
	}
	return nil
}

// writeAutoLoginToken generates a fresh one-time token, writes it
// atomically (temp + rename in the same dir) to
// wp-content/.locorum/login-token, and returns the token for inclusion
// in the URL.
//
// Atomicity matters because the GUI opens the browser the moment this
// function returns: a half-written token file would let the request
// race with the writer and read partial bytes. The same-directory
// rename guarantees POSIX atomicity on every supported filesystem.
func writeAutoLoginToken(site *types.Site) (string, error) {
	if site == nil || site.FilesDir == "" {
		return "", errors.New("writeAutoLoginToken: empty site")
	}
	token, err := generatePassword(32)
	if err != nil {
		return "", err
	}
	tokenPath := filepath.Join(autoLoginAppRoot(site), filepath.FromSlash(autoLoginTokenRelPath))
	dir := filepath.Dir(tokenPath)
	if err := utils.EnsureDir(dir); err != nil {
		return "", fmt.Errorf("creating token dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".token-tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, tokenPath); err != nil {
		return "", err
	}
	committed = true
	return token, nil
}

// autoLoginAppRoot returns the docroot path on the host — site files
// dir plus public-dir suffix when set. Centralises the rule used by
// every WordPress-aware writer.
func autoLoginAppRoot(site *types.Site) string {
	if site.PublicDir != "" && site.PublicDir != "/" {
		return filepath.Join(site.FilesDir, site.PublicDir)
	}
	return site.FilesDir
}
