package render

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseProjectTemplates parses the real web/templates so a syntax error in
// any fragment fails CI rather than only at server boot.
func TestParseProjectTemplates(t *testing.T) {
	dir, _ := os.Getwd()
	var tplDir string
	for i := 0; i < 6 && dir != "/"; i++ {
		cand := filepath.Join(dir, "web", "templates")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			tplDir = cand
			break
		}
		dir = filepath.Dir(dir)
	}
	if tplDir == "" {
		t.Skip("web/templates not found from test cwd")
	}
	if _, err := New(tplDir); err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}
