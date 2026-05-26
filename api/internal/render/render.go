// Package render parses HTML templates from disk once at startup and writes
// fragments to http.ResponseWriter. Used by handlers under /ui/* — the
// htmx-shaped surface, separate from the JSON /api/v1/* surface.
package render

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
)

type Renderer struct {
	tpl *template.Template
}

// New parses every *.html under dir into one template set. Template names are
// the file's basename without extension (status_row.html -> "status_row").
func New(dir string) (*Renderer, error) {
	root := template.New("")
	matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
	if err != nil {
		return nil, fmt.Errorf("glob templates: %w", err)
	}
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", m, err)
		}
		name := filepath.Base(m)
		name = name[:len(name)-len(filepath.Ext(name))]
		if _, err := root.New(name).Parse(string(body)); err != nil {
			return nil, fmt.Errorf("parse %s: %w", m, err)
		}
	}
	return &Renderer{tpl: root}, nil
}

// HTML renders the named template to w. Errors after status code is written
// are logged-and-swallowed — htmx will see a partial response, which is
// recoverable, vs a panic which is not.
func (r *Renderer) HTML(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := r.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
