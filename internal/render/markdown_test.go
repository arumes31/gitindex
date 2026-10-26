package render

import (
	"strings"
	"testing"
)

// Raw HTML <img> tags (not just Markdown ![]() images) must be routed through
// the same-origin /img proxy, and relative ones resolved against raw.github.
func TestRewriteProxiesRawHTMLImages(t *testing.T) {
	in := `<p align="center"><img src="app/static/img/logo.png" alt="L" width="120"/></p>
<p><a href="https://x/y"><img src="https://img.shields.io/badge/x.svg" alt="b"/></a></p>`
	out := rewriteURLs(in, "https://raw.githubusercontent.com/u/r/main/", "https://github.com/u/r/blob/main/")

	if strings.Contains(out, `src="https://img.shields.io`) {
		t.Errorf("external image not proxied:\n%s", out)
	}
	if strings.Contains(out, `src="app/static`) || strings.Contains(out, `src="https://raw.githubusercontent`) {
		t.Errorf("relative image not proxied to /img:\n%s", out)
	}
	if !strings.Contains(out, `src="/img?u=`) {
		t.Errorf("expected proxied /img src, got:\n%s", out)
	}
}

// End-to-end through ReadmeHTML: both Markdown and raw-HTML images proxied.
func TestReadmeHTMLProxiesAllImages(t *testing.T) {
	md := "# Hi\n\n<p><img src=\"logo.png\"><img src=\"https://img.shields.io/x.svg\"></p>\n\n![alt](sub/pic.png)\n"
	out := ReadmeHTML(md, "u", "r", "main")

	if n := strings.Count(out, `src="/img?u=`); n != 3 {
		t.Errorf("expected 3 proxied images, got %d:\n%s", n, out)
	}
	if strings.Contains(out, `src="https://`) || strings.Contains(out, `src="logo.png`) {
		t.Errorf("found unproxied image src:\n%s", out)
	}
}

// README HTML must not reuse site classes or clobber page ids.
func TestReadmeHTMLRestrictsClassAndID(t *testing.T) {
	md := "## Install\n\n[jump](#install)\n\n<div class=\"lightbox btn\" id=\"lightbox\">x</div>\n\n```go\nfmt.Println()\n```\n"
	out := ReadmeHTML(md, "u", "r", "main")

	for _, bad := range []string{`class="lightbox`, `id="lightbox"`, `id="install"`} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %s:\n%s", bad, out)
		}
	}
	for _, want := range []string{`id="user-content-install"`, `href="#user-content-install"`, `id="user-content-lightbox"`, `class="language-go"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %s:\n%s", want, out)
		}
	}
}
