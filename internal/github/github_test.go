package github

import "testing"

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
