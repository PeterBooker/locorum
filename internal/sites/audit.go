package sites

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/orch"
)

// auditMaxBytes caps the audit log size before rotation. 10 MiB is a
// generous bound for a developer tool — at ~500 bytes per Plan, that's
// ~20k Plans of history.
const auditMaxBytes = 10 * 1024 * 1024

// auditMu serialises log writes across goroutines. Writes are infrequent
// and short, so a global mutex is the right cost/benefit tradeoff vs. a
// dedicated writer goroutine.
var auditMu sync.Mutex

// writeAuditLog appends a JSON line summarising the Plan result to
// ~/.locorum/lifecycle.log. Best-effort: write failures are logged but never
// abort the lifecycle method.
//
// Each line:
//
//	{"time":"...","plan":"start-site:foo","duration_ms":1234,
//	 "rolled_back":false,"error":"...","steps":[{"name":"x","status":"succeeded","duration_ms":12}]}
func writeAuditLog(homeDir string, res orch.Result) {
	if homeDir == "" {
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()

	logPath := filepath.Join(homeDir, ".locorum", "lifecycle.log")
	if err := rotateIfLarge(logPath, auditMaxBytes); err != nil {
		slog.Warn("audit log rotate failed", "err", err.Error())
	}

	type stepEntry struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		DurationMS int64  `json:"duration_ms"`
		Error      string `json:"error,omitempty"`
	}
	steps := make([]stepEntry, len(res.Steps))
	for i, s := range res.Steps {
		entry := stepEntry{
			Name:       s.Name,
			Status:     string(s.Status),
			DurationMS: s.Duration.Milliseconds(),
		}
		if s.Error != nil {
			entry.Error = s.Error.Error()
		}
		steps[i] = entry
	}
	type record struct {
		Time       string      `json:"time"`
		Plan       string      `json:"plan"`
		DurationMS int64       `json:"duration_ms"`
		RolledBack bool        `json:"rolled_back"`
		Error      string      `json:"error,omitempty"`
		Steps      []stepEntry `json:"steps"`
	}
	r := record{
		Time:       res.Started.UTC().Format(time.RFC3339Nano),
		Plan:       res.PlanName,
		DurationMS: res.Duration.Milliseconds(),
		RolledBack: res.RolledBack,
		Steps:      steps,
	}
	if res.FinalError != nil {
		r.Error = res.FinalError.Error()
	}

	buf, err := json.Marshal(r)
	if err != nil {
		slog.Warn("audit log marshal failed", "err", err.Error())
		return
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		slog.Warn("audit log mkdir failed", "err", err.Error())
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("audit log open failed", "err", err.Error())
		return
	}
	defer f.Close()
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		slog.Warn("audit log write failed", "err", err.Error())
	}
}

// rotateIfLarge renames logPath to logPath.1 if it exceeds maxBytes. Old
// .1 files are overwritten — the audit log is operational, not legal hold.
func rotateIfLarge(logPath string, maxBytes int64) error {
	info, err := os.Stat(logPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	return os.Rename(logPath, logPath+".1")
}
