package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("INCLUDE_FORKS", "")
	t.Setenv("INCLUDE_ARCHIVED", "")
	t.Setenv("REDIS_DB", "")
	t.Setenv("RATE_LIMIT_ENABLED", "")
	t.Setenv("RATE_LIMIT_WINDOW", "")
	t.Setenv("RATE_LIMIT_REQUESTS", "")
	t.Setenv("TRUST_PROXY", "")
	t.Setenv("README_TTL", "")
	t.Setenv("REPO_LIST_TTL", "")
	t.Setenv("IMAGE_TTL", "")

	cfg := Load()

	if cfg.GitHubUser != "arumes31" {
		t.Fatalf("GitHubUser = %q, want arumes31", cfg.GitHubUser)
	}
	if cfg.RateLimitEnabled != true {
		t.Fatalf("RateLimitEnabled = %v, want true", cfg.RateLimitEnabled)
	}
	if cfg.RateLimitWindow != time.Minute {
		t.Fatalf("RateLimitWindow = %s, want 1m", cfg.RateLimitWindow)
	}
	if cfg.RateLimitRequests != 120 {
		t.Fatalf("RateLimitRequests = %d, want 120", cfg.RateLimitRequests)
	}
	if cfg.TrustProxy != true {
		t.Fatalf("TrustProxy = %v, want true", cfg.TrustProxy)
	}
	if cfg.ReadmeTTL != 24*time.Hour {
		t.Fatalf("ReadmeTTL = %s, want 24h", cfg.ReadmeTTL)
	}
	if cfg.RepoListTTL != 24*time.Hour {
		t.Fatalf("RepoListTTL = %s, want 24h", cfg.RepoListTTL)
	}
	if cfg.ImageTTL != 24*time.Hour {
		t.Fatalf("ImageTTL = %s, want 24h", cfg.ImageTTL)
	}
}

func TestLoadRateLimitNormalization(t *testing.T) {
	t.Setenv("RATE_LIMIT_REQUESTS", "0")
	t.Setenv("RATE_LIMIT_WINDOW", "1ms")

	cfg := Load()

	if cfg.RateLimitRequests != 120 {
		t.Fatalf("RateLimitRequests = %d, want normalized 120", cfg.RateLimitRequests)
	}
	if cfg.RateLimitWindow != time.Second {
		t.Fatalf("RateLimitWindow = %s, want normalized 1s", cfg.RateLimitWindow)
	}
}

func TestLoadRepoListTTLMinimum(t *testing.T) {
	t.Setenv("REPO_LIST_TTL", "10m")

	cfg := Load()

	if cfg.RepoListTTL != time.Hour {
		t.Fatalf("RepoListTTL = %s, want minimum 1h", cfg.RepoListTTL)
	}
}

func TestLoadImpressumConfigured(t *testing.T) {
	t.Setenv("IMPRESSUM_NAME", "Example GmbH")
	t.Setenv("IMPRESSUM_EMAIL", "legal@example.com")
	t.Setenv("IMPRESSUM_ADDRESS", "")

	cfg := Load()

	if !cfg.ImpressumConfigured() {
		t.Fatal("ImpressumConfigured() = false, want true")
	}
	if cfg.Impressum.Address != "" {
		t.Fatalf("Impressum.Address = %q, want empty", cfg.Impressum.Address)
	}
}
