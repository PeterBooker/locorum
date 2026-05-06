package sites

import (
	"testing"

	"github.com/PeterBooker/locorum/internal/storage"
)

// TestFlattenActivity covers the wire-shape conversion. The fields are
// pure data; if this drifts, every CLI/MCP client breaks at the same
// time.
func TestFlattenActivity(t *testing.T) {
	rows := []storage.ActivityEvent{
		{
			ID:         1,
			SiteID:     "s1",
			Plan:       "start-site:demo",
			Kind:       storage.ActivityKindStart,
			Status:     storage.ActivityStatusSucceeded,
			DurationMS: 4000,
			Message:    "started",
		},
		{
			ID:     2,
			SiteID: "s1",
			Kind:   storage.ActivityKindStop,
			Status: storage.ActivityStatusFailed,
		},
	}
	out := flattenActivity(rows)
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0].Kind != "start" || out[0].Status != "succeeded" {
		t.Fatalf("row 0 fields: %+v", out[0])
	}
	if out[0].DurationMS != 4000 {
		t.Fatalf("DurationMS not preserved: %d", out[0].DurationMS)
	}
	if out[1].Status != "failed" {
		t.Fatalf("row 1 status: %q", out[1].Status)
	}
}

func TestFlattenActivity_EmptyInput(t *testing.T) {
	if got := flattenActivity(nil); got != nil {
		t.Fatalf("empty input should return nil, got %+v", got)
	}
}
