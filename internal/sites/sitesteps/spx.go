package sitesteps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/types"
)

// EnsureSPXStep prepares the per-site filesystem state SPX requires:
//
//   - <site.FilesDir>/.locorum/spx — profile-data dir, bind-mounted at
//     /var/spx/data when SPX is enabled. ChownStep later fixes the
//     ownership to the PHP UID/GID.
//   - ~/.locorum/config/php/spx-keys/<slug>.ini — single-line INI
//     fragment containing `spx.http_key = <site.SPXKey>`, mounted
//     read-only at /usr/local/etc/php/conf.d/zzzz-spx-key.ini. Owns
//     the secret value end-to-end so we never depend on the wodby
//     image's pool config to forward it as an env var (which it does
//     not by default — only a fixed allowlist of MYSQL_*/etc. vars
//     reaches $_SERVER).
//
// Disabled-state behaviour: removes any previously-written key INI so
// a stale secret never lingers on disk after a toggle-off. The data
// directory itself is preserved — captured profile reports outlive
// the toggle by design.
type EnsureSPXStep struct {
	Site    *types.Site
	HomeDir string
}

func (s *EnsureSPXStep) Name() string { return "ensure-spx" }

func (s *EnsureSPXStep) Apply(_ context.Context) error {
	if s.Site == nil {
		return nil
	}

	keyPath := docker.SPXKeyINIPath(s.HomeDir, s.Site.Slug)

	if !s.Site.SPXEnabled {
		// Best-effort cleanup. A leftover key INI cannot leak in
		// the running container because the bind mount is gated on
		// SPXEnabled too, but keeping disk in sync with the toggle
		// avoids a stale secret if a user later inspects the
		// config dir.
		if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale spx key file: %w", err)
		}
		return nil
	}

	dataDir := filepath.Join(s.Site.FilesDir, ".locorum", "spx")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("ensure spx data dir: %w", err)
	}

	// Key INI lives under ~/.locorum/, never in the user-visible site
	// directory — the secret must not get committed to a project repo.
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("ensure spx key dir: %w", err)
	}
	body := []byte("; locorum-generated — DO NOT EDIT.\n; Per-site SPX key. Regenerated on each site start.\nspx.http_key = " + s.Site.SPXKey + "\n")
	if err := os.WriteFile(keyPath, body, 0o600); err != nil {
		return fmt.Errorf("write spx key ini: %w", err)
	}
	return nil
}

// Rollback is intentionally a no-op. The data directory may already
// hold capture artefacts the user wants to keep, and the key INI is
// re-derived at every Apply — neither needs reverting on a failed
// start.
func (s *EnsureSPXStep) Rollback(_ context.Context) error { return nil }

var _ orch.Step = (*EnsureSPXStep)(nil)
