package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"sync"
)

var Debug = "false"

//go:embed static
var staticFiles embed.FS

func debugStaticFS() (fs.FS, error) {
	root, err := os.OpenRoot("internal/web/static")
	if err != nil {
		return nil, fmt.Errorf("open local static/: %w", err)
	}

	return root.FS(), nil
}

func staticFS() (fs.FS, error) {
	if Debug == "true" {
		return debugStaticFS()
	} else {
		return fs.Sub(staticFiles, "static")
	}
}

//go:embed templates/*.html
var embeddedTemplates embed.FS

func debugTemplates() (fs.FS, error) {
	root, err := os.OpenRoot("internal/web/templates")
	if err != nil {
		return nil, fmt.Errorf("open local templates: %w", err)
	}

	return root.FS(), nil
}

type templates struct {
	fs    fs.FS
	cache map[string]*template.Template
	mu    sync.Mutex
}

func newTemplates() (*templates, error) {
	if Debug == "true" {
		fs, err := debugTemplates()
		if err != nil {
			return nil, err
		}

		return &templates{fs: fs, cache: nil}, nil
	}

	fs, err := fs.Sub(embeddedTemplates, "templates")
	if err != nil {
		return nil, err
	}

	return &templates{
		fs:    fs,
		cache: make(map[string]*template.Template),
	}, nil
}

func (t *templates) get(name string) (*template.Template, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cache != nil {
		tmpl, found := t.cache[name]
		if found {
			return tmpl, nil
		}
	}

	tmpl, err := template.ParseFS(t.fs, "base.html", name+".html")
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", name, err)
	}

	if t.cache != nil {
		t.cache[name] = tmpl
	}

	return tmpl, nil
}
