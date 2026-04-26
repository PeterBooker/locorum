package hooks

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
)

// Template is a one-click hook preset surfaced in the UI's "Templates" menu.
// Templates are inserts (not replacements) — clicking one appends a new
// hook to the user's site at the appropriate event.
type Template struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Event       Event    `json:"event"`
	TaskType    TaskType `json:"taskType"`
	Command     string   `json:"command"`
	Service     string   `json:"service,omitempty"`
}

// DefaultsPath is the embedded path used by Locorum at startup.
const DefaultsPath = "config/hooks/defaults.json"

// LoadTemplates reads and validates the embedded defaults.json from the
// supplied filesystem at path. Pass DefaultsPath for production code; tests
// use a stub path under testdata/.
//
// Validation: each template's TaskType + Event combination must satisfy
// Hook.Validate. Invalid templates return an error so a packaging mistake
// is caught at startup, not surfaced as a confusing GUI error later.
func LoadTemplates(efs embed.FS, path string) ([]Template, error) {
	if path == "" {
		path = DefaultsPath
	}
	data, err := fs.ReadFile(efs, path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc struct {
		Templates []Template `json:"templates"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing defaults.json: %w", err)
	}
	for i, tpl := range doc.Templates {
		h := Hook{
			Event:    tpl.Event,
			TaskType: tpl.TaskType,
			Command:  tpl.Command,
			Service:  tpl.Service,
		}
		if err := h.Validate(); err != nil {
			return nil, fmt.Errorf("template[%d] %q invalid: %w", i, tpl.Name, err)
		}
	}
	return doc.Templates, nil
}
