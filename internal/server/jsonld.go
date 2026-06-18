package server

import (
	"encoding/json"
	"html/template"
	"time"
)

// buildJSONLD produces a schema.org JSON-LD block for the current page. It is
// generated with encoding/json (which escapes <, > and & for safe HTML
// embedding) rather than inside the template, so the output is always valid
// JSON. The script tag carries the per-request CSP nonce.
func buildJSONLD(d pageData) template.HTML {
	author := map[string]any{
		"@type": "Person",
		"name":  d.Cfg.SiteAuthor,
		"url":   "https://github.com/" + d.Cfg.GitHubUser,
	}

	var obj map[string]any
	if d.Repo != nil {
		obj = map[string]any{
			"@context":       "https://schema.org",
			"@type":          "SoftwareSourceCode",
			"name":           d.Repo.Name,
			"description":    d.Description,
			"codeRepository": d.Repo.HTMLURL,
			"url":            d.Canonical,
			"author":         author,
		}
		if d.Repo.Language != "" {
			obj["programmingLanguage"] = d.Repo.Language
		}
		if lic := d.Repo.LicenseName(); lic != "" {
			obj["license"] = lic
		}
		if !d.Repo.PushedAt.IsZero() {
			obj["dateModified"] = d.Repo.PushedAt.Format(time.RFC3339)
		}
	} else {
		obj = map[string]any{
			"@context":    "https://schema.org",
			"@type":       "WebSite",
			"name":        d.Cfg.SiteName,
			"url":         d.Cfg.SiteURL,
			"description": d.Cfg.SiteTagline,
			"author":      author,
		}
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	tag := `<script type="application/ld+json" nonce="` +
		template.HTMLEscapeString(d.Nonce) + `">` + string(b) + `</script>`
	return template.HTML(tag) //nolint:gosec // JSON is escaped; nonce is escaped
}
