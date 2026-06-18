// Package server wires HTTP routing, middleware and page rendering together.
// Handlers are split across files (handlers.go, seo.go) and middleware lives in
// middleware.go to keep each unit small and focused.
package server

import (
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"time"

	"gitindex/internal/cache"
	"gitindex/internal/config"
	"gitindex/internal/github"
	"gitindex/internal/imageproxy"
)

type Server struct {
	cfg      config.Config
	gh       *github.Client
	cache    *cache.Cache
	limiter  *rateLimiter
	tpl      *template.Template
	staticFS fs.FS
}

func New(cfg config.Config, gh *github.Client, c *cache.Cache, tpl *template.Template, staticFS fs.FS) (*Server, error) {
	return &Server{
		cfg:     cfg,
		gh:      gh,
		cache:   c,
		limiter: newRateLimiter(cfg.RateLimitWindow, cfg.RateLimitRequests),
		tpl:      tpl,
		staticFS: staticFS,
	}, nil
}

// Routes builds the mux and wraps it with security-header middleware.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))))
	mux.Handle("GET /img", imageproxy.New(s.cache, s.cfg.ImageTTL))

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /repo/{name}", s.handleRepo)
	mux.HandleFunc("GET /impressum", s.handleImpressum)
	mux.HandleFunc("GET /datenschutz", s.handleDatenschutz)

	mux.HandleFunc("GET /robots.txt", s.handleRobots)
	mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.HandleFunc("/", s.handleNotFound)

	handler := http.Handler(mux)
	if s.cfg.RateLimitEnabled {
		handler = s.rateLimit(handler)
	}
	return s.securityHeaders(handler)
}

// pageData is the view model passed to every template.
type pageData struct {
	Cfg                 config.Config
	ImpressumConfigured bool
	Year                int
	Nonce               string

	Canonical   string
	Title       string
	Description string
	NoIndex     bool
	OGImage     string
	JSONLD      template.HTML

	Repos         []github.Repo
	RepoListReady bool
	Repo          *github.Repo
	ReadmeOK      bool
	Readme        template.HTML
	Message       string
}

func (s *Server) base(r *http.Request) pageData {
	// og:image is the GitHub avatar served through our own same-origin proxy,
	// so social cards work without leaking the visitor to a third party.
	avatar := "https://github.com/" + s.cfg.GitHubUser + ".png"
	return pageData{
		Cfg:                 s.cfg,
		ImpressumConfigured: s.cfg.ImpressumConfigured(),
		Year:                time.Now().Year(),
		Nonce:               nonceFrom(r.Context()),
		OGImage:             s.cfg.SiteURL + "/img?u=" + url.QueryEscape(avatar),
	}
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data pageData) {
	data.JSONLD = buildJSONLD(data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("[error] render %s: %v", name, err)
	}
}
