package hooks

import (
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func BenchmarkBuildEnv(b *testing.B) {
	site := &types.Site{
		ID:           "bench",
		Name:         "Bench Site",
		Slug:         "bench-site",
		Domain:       "bench-site.localhost",
		FilesDir:     "/srv/sites/bench-site",
		PublicDir:    "/",
		PHPVersion:   "8.3",
		MySQLVersion: "8.4",
		RedisVersion: "7",
		WebServer:    "nginx",
		Multisite:    "subdomain",
		DBPassword:   "p",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildEnv(site, ContextContainer)
	}
}
