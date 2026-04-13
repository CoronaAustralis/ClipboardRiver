package app

import (
	"embed"
	"html/template"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

func parseTemplates() (*template.Template, error) {
	return template.New("root").Funcs(template.FuncMap{
		"t": func(fn func(string) string, key string) string {
			if fn == nil {
				return key
			}
			return fn(key)
		},
		"formatTime": func(t *time.Time) string {
			if t == nil {
				return "-"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"formatTimeValue": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Local().Format("2006-01-02 15:04:05")
		},
		"checked": func(v bool) string {
			if v {
				return "checked"
			}
			return ""
		},
		"eq": func(a, b any) bool {
			return a == b
		},
	}).ParseFS(templateFS, "templates/*.html")
}
