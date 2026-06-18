// Package config loads all runtime configuration from environment variables.
// Personal / legal data (Impressum) is intentionally kept out of source.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Impressum struct {
	Name    string
	Address string
	Email   string
	Phone   string
	Extra   string // free-form extra lines, "|"-separated (e.g. VAT id)
}

type Config struct {
	Port string

	GitHubUser      string
	GitHubToken     string
	IncludeForks    bool
	IncludeArchived bool

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	RateLimitEnabled  bool
	RateLimitWindow   time.Duration
	RateLimitRequests int
	TrustProxy        bool

	// README cache TTL — must be at least 24h per project requirement.
	ReadmeTTL time.Duration
	// Repo-list cache TTL and background refresh cadence.
	RepoListTTL time.Duration
	// Proxied-image cache TTL.
	ImageTTL time.Duration

	SiteURL     string
	SiteName    string
	SiteTagline string
	SiteAuthor  string
	SiteLocale  string

	Impressum Impressum
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def, min time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			if d < min {
				return min
			}
			return d
		}
	}
	return def
}

func Load() Config {
	const day = 24 * time.Hour
	cfg := Config{
		Port: env("PORT", "6541"),

		GitHubUser:      env("GITHUB_USER", "arumes31"),
		GitHubToken:     func() string {
			t := env("GITHUB_TOKEN", "")
			if t == "github_pat_antigravitydummytoken" {
				return ""
			}
			return t
		}(),
		IncludeForks:    envBool("INCLUDE_FORKS", false),
		IncludeArchived: envBool("INCLUDE_ARCHIVED", false),

		RedisAddr:     env("REDIS_ADDR", "redis:6379"),
		RedisPassword: env("REDIS_PASSWORD", ""),
		RedisDB:       envInt("REDIS_DB", 0),

		RateLimitEnabled:  envBool("RATE_LIMIT_ENABLED", true),
		RateLimitWindow:   envDuration("RATE_LIMIT_WINDOW", time.Minute, time.Second),
		RateLimitRequests: envInt("RATE_LIMIT_REQUESTS", 120),
		TrustProxy:        envBool("TRUST_PROXY", true),

		// Floor of 24h enforced for READMEs per requirement.
		ReadmeTTL:   envDuration("README_TTL", day, day),
		RepoListTTL: envDuration("REPO_LIST_TTL", day, time.Hour),
		ImageTTL:    envDuration("IMAGE_TTL", day, time.Hour),

		SiteURL:     strings.TrimRight(env("SITE_URL", "http://localhost:6541"), "/"),
		SiteName:    env("SITE_NAME", "arumes31 · Open Source Projects"),
		SiteTagline: env("SITE_TAGLINE", ""),
		SiteAuthor:  env("SITE_AUTHOR", "arumes31"),
		SiteLocale:  env("SITE_LOCALE", "en_US"),

		Impressum: Impressum{
			Name:    env("IMPRESSUM_NAME", ""),
			Address: env("IMPRESSUM_ADDRESS", ""),
			Email:   env("IMPRESSUM_EMAIL", ""),
			Phone:   env("IMPRESSUM_PHONE", ""),
			Extra:   env("IMPRESSUM_EXTRA", ""),
		},
	}
	if cfg.RateLimitRequests < 1 {
		cfg.RateLimitRequests = 120
	}
	if cfg.RateLimitWindow < time.Second {
		cfg.RateLimitWindow = time.Second
	}
	return cfg
}

func (c Config) ImpressumConfigured() bool {
	return c.Impressum.Name != "" && c.Impressum.Email != ""
}
