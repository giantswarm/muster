package admin

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// viewSet is a parsed set of per-view template roots. Each view is its own
// *template.Template because the views share block names ("content") that
// would otherwise collide when parsed into a single template.
type viewSet map[string]*template.Template

// views lists every per-view template file. The layout is parsed into each
// root so that {{template "layout" .}} is available.
var views = []string{"list.html.tmpl", "detail.html.tmpl", "mcps.html.tmpl", "mcp_detail.html.tmpl"}

// parseTemplates loads the shared layout plus one root per view.
func parseTemplates() (viewSet, error) {
	set := viewSet{}
	for _, name := range views {
		t, err := template.New(name).Funcs(templateFuncs).ParseFS(templateFS,
			"templates/layout.html.tmpl",
			"templates/"+name,
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		set[name] = t
	}
	return set, nil
}

// staticHandler serves embedded static assets under /static/.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// This only fails if the embed directive is wrong — a programming
		// error, not a runtime condition.
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
