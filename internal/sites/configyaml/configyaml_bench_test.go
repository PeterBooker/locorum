package configyaml

import (
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func BenchmarkRender(b *testing.B) {
	site := types.Site{
		ID:         "bench",
		Name:       "Bench",
		Slug:       "bench",
		Domain:     "bench.localhost",
		FilesDir:   "/srv/sites/bench",
		PublicDir:  "/",
		PHPVersion: "8.3",
		WebServer:  "nginx",
		DBEngine:   "mysql",
		DBVersion:  "8.4",
	}
	f := FromSite(site, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Render(f); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse(b *testing.B) {
	const body = `schema_version: 1
name: Bench
slug: bench
domain: bench.localhost
php_version: "8.3"
web_server: nginx
db:
  engine: mysql
  version: "8.4"
multisite: ""
`
	data := []byte(body)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(data); err != nil {
			b.Fatal(err)
		}
	}
}
