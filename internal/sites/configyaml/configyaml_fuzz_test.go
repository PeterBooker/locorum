package configyaml

import (
	"testing"
)

// Successful parses must round-trip through Render unchanged.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"schema_version: 1\n",
		`schema_version: 1
name: test
slug: test
domain: test.localhost
php_version: "8.3"
web_server: nginx
db:
  engine: mysql
  version: "8.4"
multisite: ""
`,
		"schema_version: 999",
		"name: test\n  bogus: nesting",
		`!!str
`,
		`---
---
`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		res, err := Parse(data)
		if err != nil {
			return
		}

		rendered, rerr := Render(res.File)
		if rerr != nil {
			t.Fatalf("Render after Parse: %v", rerr)
		}
		res2, err := Parse(rendered)
		if err != nil {
			t.Fatalf("re-parse after Render failed: %v\nrendered:\n%s", err, rendered)
		}
		// Compare load-bearing fields directly; reflect.DeepEqual would
		// trip on warning slices and slice identity differences.
		if res.File.Name != res2.File.Name ||
			res.File.Slug != res2.File.Slug ||
			res.File.Domain != res2.File.Domain ||
			res.File.PHPVersion != res2.File.PHPVersion ||
			res.File.WebServer != res2.File.WebServer ||
			res.File.DB.Engine != res2.File.DB.Engine ||
			res.File.DB.Version != res2.File.DB.Version ||
			res.File.Multisite != res2.File.Multisite {
			t.Fatalf("round-trip mismatch:\nfirst:  %+v\nsecond: %+v", res.File, res2.File)
		}
	})
}
