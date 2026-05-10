package sites

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsEmptyForWordPress guards the regression where AddSite's
// .locorum/config.yaml projection caused ensureWordPress to skip the WP
// download — leaving sites with wp-config.php but no core, serving
// 403/404. The .locorum/ sentinel directory is structurally Locorum-
// owned and must not gate the download decision.
func TestIsEmptyForWordPress(t *testing.T) {
	tests := []struct {
		name  string
		seed  func(t *testing.T, dir string)
		empty bool
	}{
		{
			name:  "fresh dir",
			seed:  func(*testing.T, string) {},
			empty: true,
		},
		{
			name: "only .locorum sentinel — must not block download",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, ".locorum"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(dir, ".locorum", "config.yaml"),
					[]byte("name: x\n"), 0o644,
				); err != nil {
					t.Fatal(err)
				}
			},
			empty: true,
		},
		{
			name: "user file present — must block download",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(
					filepath.Join(dir, "composer.json"), []byte(`{}`), 0o644,
				); err != nil {
					t.Fatal(err)
				}
			},
			empty: false,
		},
		{
			name: ".locorum + user file — user file still blocks",
			seed: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(dir, ".locorum"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(dir, "wp-content"), nil, 0o644,
				); err != nil {
					t.Fatal(err)
				}
			},
			empty: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.seed(t, dir)

			got, err := isEmptyForWordPress(dir)
			if err != nil {
				t.Fatalf("isEmptyForWordPress: %v", err)
			}
			if got != tc.empty {
				t.Errorf("isEmptyForWordPress = %v, want %v", got, tc.empty)
			}
		})
	}
}
