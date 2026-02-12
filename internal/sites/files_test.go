package sites

import (
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

func TestNginxRoot(t *testing.T) {
	fn := funcMap["NginxRoot"].(func(*types.Site) string)

	tests := []struct {
		name      string
		publicDir string
		want      string
	}{
		{"root slash", "/", "/var/www/html"},
		{"empty string", "", "/var/www/html"},
		{"subdir", "public", "/var/www/html/public"},
		{"nested subdir", "web/public", "/var/www/html/web/public"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &types.Site{PublicDir: tt.publicDir}
			got := fn(site)
			if got != tt.want {
				t.Errorf("NginxRoot(%q) = %q, want %q", tt.publicDir, got, tt.want)
			}
		})
	}
}
