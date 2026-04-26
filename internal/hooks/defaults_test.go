package hooks_test

import (
	"embed"
	"testing"

	"github.com/PeterBooker/locorum/internal/hooks"
)

//go:embed testdata/defaults_valid.json
var validDefaultsFS embed.FS

//go:embed testdata/defaults_invalid.json
var invalidDefaultsFS embed.FS

func TestLoadTemplates_Valid(t *testing.T) {
	tpls, err := hooks.LoadTemplates(validDefaultsFS, "testdata/defaults_valid.json")
	if err != nil {
		t.Fatalf("LoadTemplates: %v", err)
	}
	if len(tpls) == 0 {
		t.Fatal("expected at least one template")
	}
	for _, tpl := range tpls {
		if tpl.Name == "" {
			t.Errorf("template missing Name: %+v", tpl)
		}
		if tpl.TaskType == "" {
			t.Errorf("template missing TaskType: %+v", tpl)
		}
	}
}

func TestLoadTemplates_RejectsInvalid(t *testing.T) {
	if _, err := hooks.LoadTemplates(invalidDefaultsFS, "testdata/defaults_invalid.json"); err == nil {
		t.Error("expected error for invalid templates, got nil")
	}
}
