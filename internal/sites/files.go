package sites

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/types"
)

var funcMap = template.FuncMap{
	"UpstreamName": func(s types.Site) string {
		return s.Slug + "_upstream"
	},
	"BackendHost": func(s types.Site) string {
		return "locorum_" + s.Slug + "_php"
	},
	"BackendPort": func() int {
		return 9000
	},
}

var (
	mapTpl *template.Template
	upsTpl *template.Template
)

// writeAtomic writes data to filename via a temp file + rename
func (sm *SiteManager) writeAtomic(filename string, data []byte) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, "tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), filename)
}

func (sm *SiteManager) regenerateSnippets(sites []types.Site, mapPath, upsPath string) error {
	// render map
	var mbuf bytes.Buffer
	if err := mapTpl.Execute(&mbuf, sites); err != nil {
		return fmt.Errorf("render map: %w", err)
	}
	if err := sm.writeAtomic(mapPath, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write map: %w", err)
	}

	// render upstreams
	var ubuf bytes.Buffer
	if err := upsTpl.Execute(&ubuf, sites); err != nil {
		return fmt.Errorf("render ups: %w", err)
	}
	if err := sm.writeAtomic(upsPath, ubuf.Bytes()); err != nil {
		return fmt.Errorf("write ups: %w", err)
	}

	if err := sm.d.TestGlobalNginxConfig(); err != nil {
		return fmt.Errorf("test nginx config: %w", err)
	}

	if err := sm.d.ReloadGlobalNginx(); err != nil {
		return fmt.Errorf("reload nginx config: %w", err)
	}

	return nil
}
