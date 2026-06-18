package server

import (
	"html/template"
	"log"
	"net/http"
	"strings"

	"gitindex/internal/github"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	repos, err := s.gh.Repos(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := s.base(r)
	d.Repos = repos
	d.RepoListReady = s.gh.HasCachedRepoList(r.Context())
	d.Canonical = s.cfg.SiteURL + "/"
	d.Title = s.cfg.SiteName
	d.Description = s.cfg.SiteTagline
	w.Header().Set("Cache-Control", "public, max-age=300")
	s.render(w, http.StatusOK, "index.html", d)
}

func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := s.gh.RepoBySlug(r.Context(), r.PathValue("name"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if repo == nil {
		s.notFound(w, r)
		return
	}

	readme, err := s.gh.ReadmeHTML(r.Context(), *repo)
	if err != nil {
		log.Printf("[warn] readme for %q: %v", repo.Name, err) // #nosec G706 -- %q escapes control chars (newlines), preventing log injection
	}

	d := s.base(r)
	d.Repo = repo
	d.Readme = template.HTML(readme) // #nosec G203 -- sanitized via bluemonday in internal/render
	d.ReadmeOK = strings.TrimSpace(readme) != ""
	d.Canonical = s.cfg.SiteURL + "/repo/" + repo.Slug
	d.Title = repo.Name + " · " + s.cfg.SiteAuthor
	d.Description = repoDescription(*repo, s.cfg.SiteAuthor)
	w.Header().Set("Cache-Control", "public, max-age=300")
	s.render(w, http.StatusOK, "repo.html", d)
}

func repoDescription(repo github.Repo, author string) string {
	desc := repo.Description
	if desc == "" {
		desc = repo.Name + " — open source project by " + author + " on GitHub."
	}
	if len(desc) > 300 {
		desc = desc[:300]
	}
	return desc
}

func (s *Server) handleImpressum(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	d.Canonical = s.cfg.SiteURL + "/impressum"
	d.Title = "Impressum · " + s.cfg.SiteName
	d.Description = "Legal notice / Impressum."
	s.render(w, http.StatusOK, "impressum.html", d)
}

func (s *Server) handleDatenschutz(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	d.Canonical = s.cfg.SiteURL + "/datenschutz"
	d.Title = "Datenschutz / Privacy · " + s.cfg.SiteName
	d.Description = "Privacy policy / Datenschutzerklärung."
	s.render(w, http.StatusOK, "datenschutz.html", d)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) }

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	d := s.base(r)
	d.Canonical = s.cfg.SiteURL + "/"
	d.Title = "Not found · " + s.cfg.SiteName
	d.Description = "Page not found."
	d.NoIndex = true
	s.render(w, http.StatusNotFound, "404.html", d)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("[error] %q: %v", r.URL.Path, err) // #nosec G706 -- %q escapes control chars (newlines), preventing log injection
	d := s.base(r)
	d.Canonical = s.cfg.SiteURL + "/"
	d.Title = "Error · " + s.cfg.SiteName
	d.Description = "Something went wrong."
	d.NoIndex = true
	d.Message = err.Error()
	s.render(w, http.StatusInternalServerError, "error.html", d)
}
