package main

import (
	"testing"

	"github.com/PeterBooker/locorum/internal/hooks"
)

// TestEmbeddedHookDefaults validates that the production defaults.json
// loaded via main's //go:embed all:config directive parses cleanly and
// every template passes Hook.Validate. This is the canary that catches
// packaging mistakes like "someone added an exec hook on pre-start" before
// users hit the GUI error.
func TestEmbeddedHookDefaults(t *testing.T) {
	tpls, err := hooks.LoadTemplates(config, hooks.DefaultsPath)
	if err != nil {
		t.Fatalf("loading embedded defaults: %v", err)
	}
	if len(tpls) == 0 {
		t.Fatal("embedded defaults should not be empty")
	}
	for _, tpl := range tpls {
		if tpl.Name == "" {
			t.Errorf("template missing name: %+v", tpl)
		}
	}
}
