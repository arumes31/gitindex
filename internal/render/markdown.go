// Package render converts README markdown into safe, same-origin HTML.
//
//   - Relative links/images are resolved against the repo on GitHub.
//   - Every image — whether written as Markdown (![](…)) or as a raw HTML <img>
//     tag — is routed through the local /img proxy, so the rendered page loads
//     no third-party resources (privacy + strict CSP + caching).
//   - Output is sanitized with bluemonday before it ever reaches a browser.
package render

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	htmlrenderer "github.com/yuin/goldmark/renderer/html"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var policy = buildPolicy()

func buildPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("loading", "align", "width", "height").OnElements("img")
	p.AllowAttrs("align").OnElements("div", "p", "table", "td", "th", "h1", "h2", "h3")
	p.AllowAttrs("class", "id").Globally()
	p.AllowElements("details", "summary", "del", "ins", "sup", "sub", "kbd")
	p.AllowAttrs("type", "checked", "disabled").OnElements("input")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	// Our proxied image src and resolved links are root-relative ("/img?...").
	p.AllowRelativeURLs(true)
	p.AllowDataURIImages()
	return p
}

var schemeRe = regexp.MustCompile(`(?i)^(https?:|mailto:|tel:|#|data:|//)`)

func isRelative(s string) bool {
	return s != "" && !schemeRe.MatchString(s)
}

// ReadmeHTML renders a README's markdown to sanitized, same-origin HTML.
func ReadmeHTML(md, user, repo, branch string) string {
	if branch == "" {
		branch = "HEAD"
	}
	rawBase := "https://raw.githubusercontent.com/" + user + "/" + repo + "/" + branch + "/"
	blobBase := "https://github.com/" + user + "/" + repo + "/blob/" + branch + "/"

	gm := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(htmlrenderer.WithUnsafe()), // raw HTML kept; rewritten + sanitized below
	)
	var buf bytes.Buffer
	if err := gm.Convert([]byte(md), &buf); err != nil {
		return policy.Sanitize("<p>Failed to render README.</p>")
	}

	// Rewrite URLs across the whole document (Markdown- and raw-HTML-generated),
	// then sanitize.
	rewritten := rewriteURLs(buf.String(), rawBase, blobBase)
	return string(policy.SanitizeBytes([]byte(rewritten)))
}

// rewriteURLs walks the rendered HTML and:
//   - resolves relative <img>/<a> URLs against the repo on GitHub, and
//   - routes every <img> through the same-origin /img proxy.
func rewriteURLs(htmlStr, rawBase, blobBase string) string {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(htmlStr), ctx)
	if err != nil {
		return htmlStr
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Img:
				rewriteAttr(n, "src", func(v string) string {
					return proxify(resolveImageURL(v, rawBase))
				})
				// srcset would re-introduce external hosts; drop it.
				removeAttr(n, "srcset")
			case atom.A:
				rewriteAttr(n, "href", func(v string) string {
					if isRelative(v) {
						return blobBase + strings.TrimPrefix(v, "./")
					}
					return v
				})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}

	var out bytes.Buffer
	for _, n := range nodes {
		_ = html.Render(&out, n)
	}
	return out.String()
}

// resolveImageURL turns a relative or protocol-relative image URL into an
// absolute https URL pointing at the repo's raw content / original host.
func resolveImageURL(v, rawBase string) string {
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, "//"):
		return "https:" + v
	case isRelative(v):
		return rawBase + strings.TrimPrefix(v, "./")
	default:
		return v
	}
}

// proxify routes an absolute http(s) image URL through the same-origin proxy.
func proxify(abs string) string {
	if strings.HasPrefix(abs, "http://") || strings.HasPrefix(abs, "https://") {
		return "/img?u=" + url.QueryEscape(abs)
	}
	return abs
}

func rewriteAttr(n *html.Node, key string, fn func(string) string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = fn(n.Attr[i].Val)
			return
		}
	}
}

func removeAttr(n *html.Node, key string) {
	out := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Key != key {
			out = append(out, a)
		}
	}
	n.Attr = out
}
