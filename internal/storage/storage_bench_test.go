package storage

import (
	"fmt"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

// Sidebar calls GetSites every list refresh; regressions past ~1ms
// become user-visible jank.
func BenchmarkListSites_1000(b *testing.B) {
	st := NewTestStorage(b)
	for i := 0; i < 1000; i++ {
		s := &types.Site{
			ID:        fmt.Sprintf("id-%04d", i),
			Name:      fmt.Sprintf("Bench Site %04d", i),
			Slug:      fmt.Sprintf("bench-%04d", i),
			Domain:    fmt.Sprintf("bench-%04d.localhost", i),
			FilesDir:  "/srv/sites/bench",
			PublicDir: "/",
			DBEngine:  "mysql",
			DBVersion: "8.4",
		}
		if err := st.AddSite(s); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.GetSites(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetSetting(b *testing.B) {
	st := NewTestStorage(b)
	if err := st.SetSetting("bench.key", "bench-value"); err != nil {
		b.Fatalf("seed: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.GetSetting("bench.key"); err != nil {
			b.Fatal(err)
		}
	}
}
