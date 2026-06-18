package server

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	bingSubmitOnce sync.Once
)

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = fmt.Fprintf(w, "User-agent: *\nAllow: /\n\nSitemap: %s/sitemap.xml\n", s.cfg.SiteURL)
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	repos, err := s.gh.Repos(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	add := func(loc, lastmod, freq, prio string) {
		b.WriteString("  <url><loc>" + loc + "</loc>")
		if lastmod != "" {
			b.WriteString("<lastmod>" + lastmod + "</lastmod>")
		}
		b.WriteString("<changefreq>" + freq + "</changefreq><priority>" + prio + "</priority></url>\n")
	}

	// 1. Sitemap Date Indexing (84)
	// Find the latest update date among all repos to use as the lastmod of the homepage
	var latestTime time.Time
	for _, rp := range repos {
		if rp.PushedAt.After(latestTime) {
			latestTime = rp.PushedAt
		}
	}
	homepageLastmod := ""
	if !latestTime.IsZero() {
		homepageLastmod = latestTime.Format("2006-01-02")
	}

	add(s.cfg.SiteURL+"/", homepageLastmod, "daily", "1.0")
	add(s.cfg.SiteURL+"/impressum", "", "yearly", "0.2")
	add(s.cfg.SiteURL+"/datenschutz", "", "yearly", "0.2")
	for _, rp := range repos {
		lastmod := ""
		if !rp.PushedAt.IsZero() {
			lastmod = rp.PushedAt.Format("2006-01-02")
		}
		add(s.cfg.SiteURL+"/repo/"+rp.Slug, lastmod, "weekly", "0.8")
	}

	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))

	// 2. Automatic Sitemap Submission (100)
	// Submit sitemap to Bing in the background once per runtime.
	bingSubmitOnce.Do(func() {
		sitemapURL := s.cfg.SiteURL + "/sitemap.xml"
		go func() {
			time.Sleep(3 * time.Second) // wait for server to start serving
			pingURL := fmt.Sprintf("https://www.bing.com/ping?sitemap=%s", sitemapURL)
			resp, err := http.Get(pingURL)
			if err != nil {
				log.Printf("[info] automatic sitemap submission to Bing failed: %v", err)
				return
			}
			_ = resp.Body.Close()
			log.Printf("[info] automatic sitemap submission to Bing succeeded with status: %s", resp.Status)
		}()
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}
