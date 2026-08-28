package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitindex/internal/config"
)

type memoryCache struct {
	values map[string][]byte
}

func (c *memoryCache) GetBytes(_ context.Context, key string) ([]byte, bool) {
	value, ok := c.values[key]
	return value, ok
}

func (c *memoryCache) SetBytes(_ context.Context, key string, value []byte, _ time.Duration) {
	c.values[key] = append([]byte(nil), value...)
}

func (c *memoryCache) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	if _, exists := c.values[key]; exists {
		return false, nil
	}
	c.values[key] = []byte(value)
	return true, nil
}

func (c *memoryCache) Del(_ context.Context, key string) {
	delete(c.values, key)
}

func TestCorruptRepoIndexIsRebuilt(t *testing.T) {
	t.Parallel()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/example/repos":
			_, _ = w.Write([]byte(`[{"name":"project","stargazers_count":3}]`))
		case "/search/issues":
			_, _ = w.Write([]byte(`{"total_count":0,"items":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiServer.Close()

	store := &memoryCache{values: map[string][]byte{repoListCacheKey: []byte("not-json")}}
	client := &Client{
		cfg:    config.Config{GitHubUser: "example", RepoListTTL: time.Hour},
		cache:  store,
		http:   &http.Client{Timeout: time.Second},
		apiURL: apiServer.URL,
	}

	repos, err := client.allRepos(context.Background())
	if err != nil {
		t.Fatalf("allRepos() error = %v", err)
	}
	if len(repos) != 1 || repos[0].Slug != "project" {
		t.Fatalf("allRepos() = %#v, want rebuilt project index", repos)
	}
	if _, ok := client.cachedRepoList(context.Background(), repoListCacheKey); !ok {
		t.Fatal("rebuilt repository index was not cached")
	}
}

func TestNewClientHasOutboundDeadline(t *testing.T) {
	t.Parallel()
	client := New(config.Config{}, nil)
	if client.http.Timeout != 15*time.Second {
		t.Fatalf("HTTP timeout = %s, want 15s", client.http.Timeout)
	}
}

func TestOpenIssuesOnly(t *testing.T) {
	tests := []struct {
		name       string
		repo       Repo
		wantIssues int
	}{
		{
			name:       "excludes pull requests",
			repo:       Repo{OpenIssues: 8, OpenPRs: 3},
			wantIssues: 5,
		},
		{
			name:       "does not go negative",
			repo:       Repo{OpenIssues: 1, OpenPRs: 4},
			wantIssues: 0,
		},
		{
			name:       "zero values",
			repo:       Repo{},
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.repo.OpenIssuesOnly(); got != tt.wantIssues {
				t.Fatalf("OpenIssuesOnly() = %d, want %d", got, tt.wantIssues)
			}
		})
	}
}

func TestNextLink(t *testing.T) {
	header := `<https://api.github.com/user/repos?page=2>; rel="next", <https://api.github.com/user/repos?page=5>; rel="last"`

	got := nextLink(header)
	want := "https://api.github.com/user/repos?page=2"
	if got != want {
		t.Fatalf("nextLink() = %q, want %q", got, want)
	}
}

func TestNextLinkMissing(t *testing.T) {
	if got := nextLink(`<https://api.github.com/user/repos?page=5>; rel="last"`); got != "" {
		t.Fatalf("nextLink() = %q, want empty", got)
	}
}

func TestLastPage(t *testing.T) {
	header := `<https://api.github.com/user/repos?page=1>; rel="first", <https://api.github.com/user/repos?page=5>; rel="last"`

	if got := lastPage(header); got != 5 {
		t.Fatalf("lastPage() = %d, want 5", got)
	}
}

func TestLastPageMissing(t *testing.T) {
	if got := lastPage(`<https://api.github.com/user/repos?page=2>; rel="next"`); got != 0 {
		t.Fatalf("lastPage() = %d, want 0", got)
	}
}

func TestLastPageInvalidURL(t *testing.T) {
	header := `<https://api.github.com/user/repos?page=not-a-number>; rel="last"`

	if got := lastPage(header); got != 0 {
		t.Fatalf("lastPage() = %d, want 0 for invalid page", got)
	}
}
