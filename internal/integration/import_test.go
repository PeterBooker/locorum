//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/sites"
)

// Filter logic is unit-tested in import_filters_test.go; this exercises
// the streaming path against a real DB container.
func TestImport_StripsCollation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	id := mustCreateAndStart(t, h, "imp")

	dumpPath := filepath.Join(t.TempDir(), "import.sql")
	const body = `/*!999999\- enable the sandbox mode */;
DROP TABLE IF EXISTS wp_import_check;
CREATE TABLE wp_import_check (id int) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_uca1400_ai_ci;
INSERT INTO wp_import_check VALUES (42);
`
	if err := os.WriteFile(dumpPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write dump: %v", err)
	}

	impCtx, cancel := context.WithTimeout(h.ctx, 3*time.Minute)
	defer cancel()
	if err := h.sites.ImportDB(impCtx, id, dumpPath, sites.ImportDBOptions{}); err != nil {
		t.Fatalf("ImportDB: %v", err)
	}

	got, err := h.sites.ExecWPCLI(h.ctx, id, []string{"db", "query", "SELECT id FROM wp_import_check WHERE id = 42"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(got, "42") {
		t.Errorf("expected imported row 42; got %q", got)
	}

	stop := timeoutCtx(t, h.ctx, 60*time.Second)
	_ = h.sites.StopSite(stop, id)
}
