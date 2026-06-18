package main

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"strings"
	"time"
)

//go:embed web/templates/*.html
var templatesFS embed.FS

//go:embed web/static/*
var staticFS embed.FS

// Templates parses all embedded HTML templates with shared helper funcs.
func Templates() *template.Template {
	return template.Must(
		template.New("").Funcs(funcMap()).ParseFS(templatesFS, "web/templates/*.html"),
	)
}

// StaticFS returns the embedded static assets rooted at web/static, so they are
// served at /static/<file> instead of /web/static/<file>.
func StaticFS() fs.FS {
	sub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		log.Fatalf("static sub fs: %v", err)
	}
	return sub
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"shortDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02")
		},
		"iso": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format(time.RFC3339)
		},
		"lower": strings.ToLower,
	}
}
