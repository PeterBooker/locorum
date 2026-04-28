package sites

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/types"
)

// inContainerWPPath returns the absolute path to the WordPress install
// inside the PHP container — either /var/www/html or that path plus the
// site's docroot subdirectory. wp-cli auto-detects from CWD but pinning
// `--path` is more deterministic across cron / hook contexts.
func inContainerWPPath(site *types.Site) string {
	doc := normaliseInContainerDocroot(site.PublicDir)
	if doc == "" {
		return "/var/www/html"
	}
	return "/var/www/html/" + doc
}

// normaliseInContainerDocroot mirrors docker.normaliseDocroot but the
// docker package does not export it; duplicating the small function is
// preferable to widening the docker package's API surface for one caller.
func normaliseInContainerDocroot(publicDir string) string {
	if publicDir == "" || publicDir == "/" || publicDir == "." {
		return ""
	}
	for len(publicDir) > 0 && publicDir[0] == '/' {
		publicDir = publicDir[1:]
	}
	return publicDir
}

// wpcli runs `wp <args...> --path=<in-container WP path>` inside the PHP
// container. WP_CLI_ALLOW_ROOT is already set on the container env (see
// docker.PHPSpec) so callers do NOT need to add --allow-root.
func (sm *SiteManager) wpcli(ctx context.Context, site *types.Site, args ...string) (string, error) {
	if site == nil {
		return "", errors.New("wpcli: nil site")
	}
	cmd := make([]string, 0, len(args)+2)
	cmd = append(cmd, "wp")
	cmd = append(cmd, args...)
	cmd = append(cmd, "--path="+inContainerWPPath(site))
	out, err := sm.d.ExecInContainer(ctx, docker.SiteContainerName(site.Slug, "php"), cmd)
	if err != nil {
		return out, fmt.Errorf("wp %s: %w", args[0], err)
	}
	return out, nil
}

// wpIsInstalled returns (installed, networkInstalled, err). The two
// queries are cheap (~50ms each); doing both in one helper avoids
// duplicating the failure-handling logic at every caller.
func (sm *SiteManager) wpIsInstalled(ctx context.Context, site *types.Site) (single, network bool) {
	// Exit code 0 = installed, anything else = not. We deliberately ignore
	// the error here because wp-cli signals "not installed" via non-zero
	// exit, which Exec returns as an error.
	if _, err := sm.wpcli(ctx, site, "core", "is-installed"); err == nil {
		single = true
	}
	if _, err := sm.wpcli(ctx, site, "core", "is-installed", "--network"); err == nil {
		network = true
	}
	return
}

// wpInstallDefault performs the canonical first-run `wp core install`
// using Locorum's local-dev defaults. Idempotent — short-circuits if
// already installed.
func (sm *SiteManager) wpInstallDefault(ctx context.Context, site *types.Site) error {
	if single, _ := sm.wpIsInstalled(ctx, site); single {
		return nil
	}
	_, err := sm.wpcli(ctx, site,
		"core", "install",
		"--url=https://"+site.Domain,
		"--title="+site.Name,
		"--admin_user=admin",
		"--admin_password=admin",
		"--admin_email=admin@"+site.Domain,
		"--skip-email",
	)
	return err
}

// wpSearchReplace runs `wp search-replace <from> <to> --all-tables` and
// `--skip-columns=guid` (the WP-recommended invariant for URL rewrites:
// guid is a permanent identifier, not a URL, despite looking like one).
// Returns the wp-cli output verbatim so the caller can surface row counts.
func (sm *SiteManager) wpSearchReplace(ctx context.Context, site *types.Site, from, to string) (string, error) {
	if from == "" || to == "" {
		return "", errors.New("wpSearchReplace: from and to are required")
	}
	if from == to {
		return "", nil
	}
	return sm.wpcli(ctx, site,
		"search-replace", from, to,
		"--all-tables",
		"--skip-columns=guid",
	)
}

// wpDBImport runs `wp db import <inContainerPath>`. The caller is
// responsible for placing the dump where wp-cli can read it (typically
// inside the bind-mounted FilesDir).
func (sm *SiteManager) wpDBImport(ctx context.Context, site *types.Site, inContainerPath string) (string, error) {
	if inContainerPath == "" {
		return "", errors.New("wpDBImport: empty path")
	}
	return sm.wpcli(ctx, site, "db", "import", inContainerPath)
}

// wpOptionGet returns the value of a WordPress option (e.g. "siteurl",
// "home"). Empty string + nil means the option is not set.
func (sm *SiteManager) wpOptionGet(ctx context.Context, site *types.Site, key string) (string, error) {
	out, err := sm.wpcli(ctx, site, "option", "get", key)
	if err != nil {
		return "", err
	}
	// wp-cli outputs the value with a trailing newline.
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out, nil
}

// wpMultisiteConvert wraps `wp core multisite-convert`. Mode is "subdomain"
// or "subdirectory"; an empty string is treated as a no-op caller bug.
func (sm *SiteManager) wpMultisiteConvert(ctx context.Context, site *types.Site, mode string) error {
	args := []string{"core", "multisite-convert", "--title=" + site.Name}
	if mode == "subdomain" {
		args = append(args, "--subdomains")
	}
	_, err := sm.wpcli(ctx, site, args...)
	return err
}

// detectWordPress searches for wp-settings.php under the site's docroot
// at depth 0 (the docroot itself) and depth 1 (immediate subdirectories).
// Two-deep matches DDEV's behaviour and catches the common
// "uploaded the WP zip into a subfolder" case. More than one match is
// treated as ambiguous and surfaced as an error — guessing here causes
// the next start to wedge against the wrong install.
//
// Returns the relative paths (from filesDir) of the directories containing
// wp-settings.php. Empty slice + nil error means "no WordPress yet" — the
// caller decides whether to download a fresh copy or to bail.
func detectWordPress(filesDir, publicDir string) ([]string, error) {
	root := filesDir
	if doc := normaliseInContainerDocroot(publicDir); doc != "" {
		root = filepath.Join(filesDir, doc)
	}

	// Depth 0: the docroot itself.
	var matches []string
	if _, err := os.Stat(filepath.Join(root, "wp-settings.php")); err == nil {
		matches = append(matches, ".")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat docroot: %w", err)
	}

	// Depth 1: immediate subdirectories. Skip dot-dirs (e.g. .git) and
	// vendor caches that can't possibly host a WP install — keeps the
	// search predictable and fast on large trees.
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read docroot: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		switch name {
		case "node_modules", "vendor", "wp-content", "wp-includes", "wp-admin":
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, "wp-settings.php")); err == nil {
			matches = append(matches, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("stat %q: %w", name, err)
		}
	}

	if len(matches) > 1 {
		return matches, fmt.Errorf("ambiguous WordPress install: found wp-settings.php in %v under %s — Locorum refuses to guess; remove the extra copy or set the site's PublicDir to the correct subdirectory", matches, root)
	}
	return matches, nil
}
