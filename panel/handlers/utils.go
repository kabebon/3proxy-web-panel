package handlers

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
)

var tmpls *template.Template

// InitTemplates loads all templates from the templates directory.
func InitTemplates(dir string) error {
	pattern := filepath.Join(dir, "*.html")
	t, err := template.New("").Funcs(templateFuncs()).ParseGlob(pattern)
	if err != nil {
		return err
	}
	// Parse sub-directories
	for _, sub := range []string{"users", "upstreams", "groups", "listeners", "stats", "partials"} {
		subPattern := filepath.Join(dir, sub, "*.html")
		matches, _ := filepath.Glob(subPattern)
		if len(matches) > 0 {
			t, err = t.ParseGlob(subPattern)
			if err != nil {
				return err
			}
		}
	}
	tmpls = t
	return nil
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"derefInt": func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		},
	}
}

// render renders a template.
// For htmx requests: renders just the named template with data.
// For full-page requests: renders content into buffer, passes as .Content to layout.html.
// data should be a map[string]any — its keys are merged with layout keys (CSRFToken, CurrentPage, Content).
func render(w http.ResponseWriter, r *http.Request, tmplName string, data any) {
	if tmpls == nil {
		log.Println("[warn] templates not initialized")
		http.Error(w, "templates not initialized", 500)
		return
	}

	isHTMX := r.Header.Get("HX-Request") == "true"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	csrfToken := ""
	if c, err := r.Cookie("csrf_token"); err == nil {
		csrfToken = c.Value
	}

	// Build page payload: merge data map with common fields
	pagePayload := buildPayload(data, csrfToken, tmplName)

	if isHTMX {
		if err := tmpls.ExecuteTemplate(w, tmplName, pagePayload); err != nil {
			log.Printf("[error] render htmx %s: %v", tmplName, err)
			http.Error(w, err.Error(), 500)
		}
		return
	}

	// Render content into buffer
	var buf bytes.Buffer
	if err := tmpls.ExecuteTemplate(&buf, tmplName, pagePayload); err != nil {
		log.Printf("[error] render content %s: %v", tmplName, err)
		http.Error(w, err.Error(), 500)
		return
	}

	// Render layout with .Content = rendered page HTML
	layoutPayload := map[string]any{
		"Content":     template.HTML(buf.String()), //nolint:gosec
		"CurrentPage": pageName(tmplName),
		"CSRFToken":   csrfToken,
	}
	if err := tmpls.ExecuteTemplate(w, "layout.html", layoutPayload); err != nil {
		log.Printf("[error] render layout for %s: %v", tmplName, err)
		http.Error(w, err.Error(), 500)
	}
}

// buildPayload merges data (if map[string]any) with common fields.
func buildPayload(data any, csrfToken, tmplName string) map[string]any {
	payload := map[string]any{
		"CSRFToken":   csrfToken,
		"CurrentPage": pageName(tmplName),
	}
	if m, ok := data.(map[string]any); ok {
		for k, v := range m {
			payload[k] = v
		}
	} else if data != nil {
		payload["Data"] = data
	}
	return payload
}

// renderTemplate renders a specific named template block directly (for htmx partials).
func renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpls.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("[error] renderTemplate %s: %v", name, err)
		http.Error(w, err.Error(), 500)
	}
}

// triggerToast sets HX-Trigger header to show a toast notification.
func triggerToast(w http.ResponseWriter, msg, kind string) {
	w.Header().Set("HX-Trigger", `{"showToast":{"message":"`+msg+`","type":"`+kind+`"}}`)
}

// pageName extracts a human-readable page name from template path.
func pageName(tmplName string) string {
	base := filepath.Base(tmplName)
	ext := filepath.Ext(base)
	if ext != "" {
		return base[:len(base)-len(ext)]
	}
	return base
}
