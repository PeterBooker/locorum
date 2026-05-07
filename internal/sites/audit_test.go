package sites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/utils"
)

func TestWriteAuditLog_AppendsJSONLine(t *testing.T) {
	dir := t.TempDir()
	res := orch.Result{
		PlanName: "start-site:demo",
		Started:  time.Now(),
		Duration: 250 * time.Millisecond,
		Steps: []orch.StepResult{
			{Name: "ensure-network", Status: orch.StatusSucceeded, Duration: 10 * time.Millisecond},
			{Name: "pull-images", Status: orch.StatusSucceeded, Duration: 200 * time.Millisecond},
		},
	}
	writeAuditLog(dir, res)

	data, err := os.ReadFile(filepath.Join(dir, ".locorum", "lifecycle.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "start-site:demo") {
		t.Errorf("missing plan name: %s", data)
	}
	if !strings.Contains(string(data), `"name":"ensure-network"`) {
		t.Errorf("missing step name: %s", data)
	}
}

func TestWriteAuditLog_RecordsError(t *testing.T) {
	dir := t.TempDir()
	res := orch.Result{
		PlanName:   "start-site:demo",
		Started:    time.Now(),
		Duration:   100 * time.Millisecond,
		FinalError: errors.New("boom"),
		RolledBack: true,
	}
	writeAuditLog(dir, res)

	data, err := os.ReadFile(filepath.Join(dir, ".locorum", "lifecycle.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), `"error":"boom"`) {
		t.Errorf("missing error: %s", data)
	}
	if !strings.Contains(string(data), `"rolled_back":true`) {
		t.Errorf("missing rolled_back: %s", data)
	}
}

func TestRotateIfLarge(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log")

	// Below threshold — no rotate.
	if err := os.WriteFile(logPath, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := utils.RotateIfLarge(logPath, 1024, 1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Errorf("rotated below threshold")
	}

	// Above threshold — rotate.
	big := strings.Repeat("x", 2000)
	if err := os.WriteFile(logPath, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := utils.RotateIfLarge(logPath, 1024, 1); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("rotation .1 missing: %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("original still present after rotate")
	}
}

func TestWriteAuditLog_HonoursMissingHomeDir(t *testing.T) {
	// empty homeDir — no-op (no panic, no write)
	res := orch.Result{PlanName: "x"}
	writeAuditLog("", res)
}
