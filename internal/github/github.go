// Package github fetches repositories and READMEs for a user, caching results
// in Redis. README HTML is cached for >= 24h; the repo list is refreshed in the
// background and kept as stale fallback data for longer outage windows.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitindex/internal/cache"
	"gitindex/internal/config"
	"gitindex/internal/render"
)

const (
	api = "https://api.github.com"

	repoListCacheKey     = "repos:all:v2"
	repoListStaleKey     = "repos:all:v2:stale"
	repoListRefreshLock  = "repos:all:v2:lock"
	repoListStaleTTL     = 7 * 24 * time.Hour
	repoListLockTTL      = 5 * time.Minute
	repoListRefreshDelay = 30 * time.Second
)

type Repo struct {
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	Description   string    `json:"description"`
	HTMLURL       string    `json:"html_url"`
	Homepage      string    `json:"homepage"`
	Language      string    `json:"language"`
	Topics        []string  `json:"topics"`
	Stars         int       `json:"stargazers_count"`
	Forks         int       `json:"forks_count"`
	OpenIssues    int       `json:"open_issues_count"` // NOTE: GitHub counts issues + PRs here
	OpenPRs       int       `json:"open_prs"`          // enriched via the pulls endpoint
	Archived      bool      `json:"archived"`
	Fork          bool      `json:"fork"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CreatedAt     time.Time `json:"created_at"`
	License       struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
}

func (r Repo) LicenseName() string {
	if r.License.SPDXID == "" || r.License.SPDXID == "NOASSERTION" {
		return ""
	}
	return r.License.SPDXID
}

// OpenIssuesOnly returns the open *issue* count, excluding pull requests, which
// GitHub otherwise folds into open_issues_count.
func (r Repo) OpenIssuesOnly() int {
	if n := r.OpenIssues - r.OpenPRs; n > 0 {
		return n
	}
	return 0
}

type Client struct {
	cfg    config.Config
	cache  cacheStore
	http   *http.Client
	apiURL string
}

type cacheStore interface {
	GetBytes(context.Context, string) ([]byte, bool)
	SetBytes(context.Context, string, []byte, time.Duration)
	SetNX(context.Context, string, string, time.Duration) (bool, error)
	Del(context.Context, string)
}

func New(cfg config.Config, c *cache.Cache) *Client {
	return &Client{
		cfg:    cfg,
		cache:  c,
		http:   &http.Client{Timeout: 15 * time.Second},
		apiURL: api,
	}
}

func (c *Client) StartBackgroundRefresh(ctx context.Context) {
	go func() {
		// Clear stale refresh lock at startup so it runs immediately
		c.cache.Del(ctx, repoListRefreshLock)

		delay := repoListRefreshDelay
		if !c.HasCachedRepoList(ctx) {
			delay = 100 * time.Millisecond
			log.Printf("[info] no cached repository list found. warming up cache immediately...")
		} else {
			log.Printf("[info] cached repository list found. background refresh scheduled in %v", delay)
		}

		timer := time.NewTimer(delay)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				if _, err := c.refreshRepoList(refreshCtx); err != nil {
					log.Printf("[warn] background repo refresh failed: %v", err)
				}
				cancel()
				timer.Reset(c.cfg.RepoListTTL)
			}
		}
	}()
}

func (c *Client) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "gitindex/"+c.cfg.GitHubUser)
	if c.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.GitHubToken)
	}
	return c.http.Do(req)
}

// allRepos returns the complete, unfiltered repo list (sorted + enriched),
// cached in Redis. Fork/archived filtering is applied later at read time so a
// config change takes effect on restart without needing to flush the cache.
func (c *Client) allRepos(ctx context.Context) ([]Repo, error) {
	if repos, ok := c.cachedRepoList(ctx, repoListCacheKey); ok {
		return repos, nil
	}

	refreshed, err := c.refreshRepoList(ctx)
	if err == nil {
		return refreshed, nil
	}
	log.Printf("[warn] repo list refresh failed: %v", err)

	if repos, ok := c.cachedRepoList(ctx, repoListStaleKey); ok {
		log.Printf("[warn] serving stale repo list from cache")
		return repos, nil
	}

	return nil, err
}

func (c *Client) cachedRepoList(ctx context.Context, key string) ([]Repo, bool) {
	b, ok := c.cache.GetBytes(ctx, key)
	if !ok {
		return nil, false
	}
	var repos []Repo
	if err := json.Unmarshal(b, &repos); err != nil {
		log.Printf("[warn] cached repo list %q is invalid: %v", key, err)
		return nil, false
	}
	return repos, true
}

func (c *Client) refreshRepoList(ctx context.Context) ([]Repo, error) {
	if !c.acquireRefreshLock(ctx) {
		return nil, fmt.Errorf("repo list refresh already running")
	}
	defer c.cache.Del(context.Background(), repoListRefreshLock)

	repos, err := c.fetchAllRepos(ctx)
	if err != nil {
		return nil, err
	}
	for i := range repos {
		repos[i].Slug = strings.ToLower(repos[i].Name)
	}
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Stars != repos[j].Stars {
			return repos[i].Stars > repos[j].Stars
		}
		return repos[i].PushedAt.After(repos[j].PushedAt)
	})

	c.enrichPRCounts(ctx, repos)

	b, err := json.Marshal(repos)
	if err != nil {
		return nil, err
	}
	c.cache.SetBytes(ctx, repoListCacheKey, b, c.cfg.RepoListTTL)
	c.cache.SetBytes(ctx, repoListStaleKey, b, repoListStaleTTL)
	return repos, nil
}

func (c *Client) acquireRefreshLock(ctx context.Context) bool {
	ok, err := c.cache.SetNX(ctx, repoListRefreshLock, "1", repoListLockTTL)
	if err != nil {
		log.Printf("[cache] refresh lock failed: %v", err)
		return true
	}
	return ok
}

func (c *Client) HasCachedRepoList(ctx context.Context) bool {
	if _, ok := c.cache.GetBytes(ctx, repoListCacheKey); ok {
		return true
	}
	_, ok := c.cache.GetBytes(ctx, repoListStaleKey)
	return ok
}

// Repos returns the repo list filtered by the current fork/archived config.
func (c *Client) Repos(ctx context.Context) ([]Repo, error) {
	all, err := c.allRepos(ctx)
	if err != nil {
		log.Printf("[warn] repo list unavailable, showing empty project list: %v", err)
		return nil, nil
	}
	out := make([]Repo, 0, len(all))
	for _, r := range all {
		if r.Fork && !c.cfg.IncludeForks {
			continue
		}
		if r.Archived && !c.cfg.IncludeArchived {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (c *Client) fetchAllRepos(ctx context.Context) ([]Repo, error) {
	var all []Repo
	url := fmt.Sprintf("%s/users/%s/repos?per_page=100&sort=updated", c.apiURL, c.cfg.GitHubUser)
	for url != "" {
		resp, err := c.do(ctx, url)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github repos: %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		var page []Repo
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		url = nextLink(resp.Header.Get("Link"))
	}
	return all, nil
}

type searchItem struct {
	RepositoryURL string `json:"repository_url"`
}

type searchResponse struct {
	TotalCount int          `json:"total_count"`
	Items      []searchItem `json:"items"`
}

// enrichPRCounts populates Repo.OpenPRs for each repo. It uses the GitHub Search API
// to fetch all open PRs in a single query (or up to 2 requests for 100+ items), preventing
// rate limit exhaustion. It falls back to per-repository queries if the Search API fails.
func (c *Client) enrichPRCounts(ctx context.Context, repos []Repo) {
	repoPRs := make(map[string]int)
	username := c.cfg.GitHubUser
	page := 1

	for {
		reqUrl := fmt.Sprintf("%s/search/issues?q=user:%s%%20is:open%%20is:pr&per_page=100&page=%d", c.apiURL, url.QueryEscape(username), page)
		resp, err := c.do(ctx, reqUrl)
		if err != nil {
			log.Printf("[warn] search open PRs failed: %v, falling back to per-repo pulls endpoint", err)
			c.enrichPRCountsFallback(ctx, repos)
			return
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Printf("[warn] search open PRs API returned status %s: %s, falling back to per-repo pulls endpoint", resp.Status, string(body))
			c.enrichPRCountsFallback(ctx, repos)
			return
		}

		var sResp searchResponse
		if err := json.Unmarshal(body, &sResp); err != nil {
			log.Printf("[warn] unmarshal search open PRs failed: %v, falling back to per-repo pulls endpoint", err)
			c.enrichPRCountsFallback(ctx, repos)
			return
		}

		for _, item := range sResp.Items {
			parts := strings.Split(item.RepositoryURL, "/")
			if len(parts) > 0 {
				repoName := strings.ToLower(parts[len(parts)-1])
				repoPRs[repoName]++
			}
		}

		if len(sResp.Items) < 100 || page*100 >= sResp.TotalCount {
			break
		}
		page++
	}

	for i := range repos {
		repos[i].OpenPRs = repoPRs[strings.ToLower(repos[i].Name)]
	}
	log.Printf("[info] successfully enriched PR counts for %d repos using Search API", len(repos))
}

// enrichPRCountsFallback populates Repo.OpenPRs using the token-gated concurrent pulls method
func (c *Client) enrichPRCountsFallback(ctx context.Context, repos []Repo) {
	if c.cfg.GitHubToken == "" {
		log.Printf("[info] skipping open PR enrichment because GITHUB_TOKEN is empty")
		return
	}

	const batch = 4
	for i := 0; i < len(repos); i += batch {
		end := i + batch
		if end > len(repos) {
			end = len(repos)
		}
		var wg sync.WaitGroup
		for j := i; j < end; j++ {
			wg.Add(1)
			go func(r *Repo) {
				defer wg.Done()
				if n, err := c.openPRCount(ctx, r.Name); err == nil {
					r.OpenPRs = n
				}
			}(&repos[j])
		}
		wg.Wait()
	}
}

// openPRCount returns the number of open pull requests for a repo. It requests a
// single-item page and reads the count from the rel="last" pagination link,
// avoiding the need to download every PR.
func (c *Client) openPRCount(ctx context.Context, repo string) (int, error) {
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/pulls?state=open&per_page=1", c.apiURL, c.cfg.GitHubUser, repo))
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github pulls %s: %s", repo, resp.Status)
	}
	if last := lastPage(resp.Header.Get("Link")); last > 0 {
		return last, nil
	}
	// No pagination: at most one item on this page.
	var arr []struct{}
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		return 0, err
	}
	return len(arr), nil
}

// RepoBySlug returns a single repo from the cached list.
func (c *Client) RepoBySlug(ctx context.Context, slug string) (*Repo, error) {
	repos, err := c.Repos(ctx)
	if err != nil {
		return nil, err
	}
	slug = strings.ToLower(slug)
	for i := range repos {
		if repos[i].Slug == slug {
			return &repos[i], nil
		}
	}
	return nil, nil
}

// ReadmeHTML returns rendered, sanitized README HTML (Redis-cached >= 24h).
func (c *Client) ReadmeHTML(ctx context.Context, r Repo) (string, error) {
	key := "readme:html:v2:" + strings.ToLower(r.Name)
	if b, ok := c.cache.GetBytes(ctx, key); ok {
		return string(b), nil
	}

	md, err := c.fetchReadmeMarkdown(ctx, r.Name)
	if err != nil {
		return "", err
	}
	var htmlOut string
	if strings.TrimSpace(md) != "" {
		htmlOut = render.ReadmeHTML(md, c.cfg.GitHubUser, r.Name, r.DefaultBranch)
	}
	// Cache even an empty result to avoid hammering the API for repos w/o README.
	c.cache.SetBytes(ctx, key, []byte(htmlOut), c.cfg.ReadmeTTL)
	return htmlOut, nil
}

func (c *Client) fetchReadmeMarkdown(ctx context.Context, repo string) (string, error) {
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/readme", c.apiURL, c.cfg.GitHubUser, repo))
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github readme %s: %s", repo, resp.Status)
	}
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Encoding == "base64" {
		raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return payload.Content, nil
}

// nextLink extracts the rel="next" URL from a GitHub Link header.
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		if strings.Contains(part, `rel="next"`) {
			if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i >= 0 && j > i {
				return part[i+1 : j]
			}
		}
	}
	return ""
}

// lastPage extracts the page number of the rel="last" URL from a Link header.
// With per_page=1 this equals the total item count. Returns 0 if absent.
func lastPage(header string) int {
	for _, part := range strings.Split(header, ",") {
		if !strings.Contains(part, `rel="last"`) {
			continue
		}
		if i, j := strings.Index(part, "<"), strings.Index(part, ">"); i >= 0 && j > i {
			if u, err := url.Parse(part[i+1 : j]); err == nil {
				if n, err := strconv.Atoi(u.Query().Get("page")); err == nil {
					return n
				}
			}
		}
	}
	return 0
}
