package hooks

import (
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

// Output must be canonical KEY=VALUE, no duplicate keys, deterministic.
func FuzzBuildEnv(f *testing.F) {
	seeds := []struct {
		name, slug, dom, php string
	}{
		{"site", "site", "site.localhost", "8.3"},
		{"", "", "", ""},
		{"a\x00b", "slug", "x.localhost", "8.2"},
		{"name", "slug-with-very-long-suffix-and-special-chars-é", "d.localhost", "8.4"},
	}
	for _, s := range seeds {
		f.Add(s.name, s.slug, s.dom, s.php)
	}
	f.Fuzz(func(t *testing.T, name, slug, dom, php string) {
		s := &types.Site{
			ID:           "id",
			Name:         name,
			Slug:         slug,
			Domain:       dom,
			PHPVersion:   php,
			MySQLVersion: "8.4",
			RedisVersion: "7",
			DBPassword:   "p",
		}
		env := BuildEnv(s, ContextContainer)
		seen := map[string]bool{}
		for _, e := range env {
			i := strings.IndexByte(e, '=')
			if i <= 0 {
				t.Fatalf("not KEY=VALUE: %q", e)
			}
			key := e[:i]
			if seen[key] {
				t.Fatalf("duplicate key %q in env", key)
			}
			seen[key] = true
			if !strings.HasPrefix(key, "LOCORUM_") {
				t.Fatalf("key %q does not start with LOCORUM_", key)
			}
		}

		env2 := BuildEnv(s, ContextContainer)
		if len(env) != len(env2) {
			t.Fatalf("non-deterministic length: %d vs %d", len(env), len(env2))
		}
		for i := range env {
			if env[i] != env2[i] {
				t.Fatalf("non-deterministic at %d: %q vs %q", i, env[i], env2[i])
			}
		}
	})
}
