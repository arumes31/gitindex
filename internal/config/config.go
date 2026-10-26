// Package config loads all runtime configuration from environment variables.
// Personal / legal data (Impressum) is intentionally kept out of source.
package config

import (
	"log"
	"net/netip"
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
	// Forwarding headers are only honored when the direct peer is in one of
	// these ranges, so clients cannot spoof their IP by sending them.
	TrustedProxies []netip.Prefix

	// HMAC secret for signing /img proxy URLs. Empty means a random secret is
	// generated at startup (proxy URLs then change on every restart).
	ImageProxySecret string

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

// defaultTrustedProxies covers reverse proxies on the same host or Docker
// network (loopback, RFC1918, IPv6 ULA).
const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

// parsePrefixes parses a comma-separated list of CIDRs or bare IPs. Invalid
// entries are logged and skipped.
func parsePrefixes(s string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if p, err := netip.ParsePrefix(part); err == nil {
			out = append(out, p.Masked())
			continue
		}
		if a, err := netip.ParseAddr(part); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		log.Printf("[warn] TRUSTED_PROXIES: ignoring invalid entry %q", part)
	}
	return out
}

func Load() Config {
	const day = 24 * time.Hour
	cfg := Config{
		Port: env("PORT", "6541"),

		GitHubUser: env("GITHUB_USER", "arumes31"),
		GitHubToken: func() string {
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
		TrustedProxies:    parsePrefixes(env("TRUSTED_PROXIES", defaultTrustedProxies)),

		ImageProxySecret: env("IMAGE_PROXY_SECRET", ""),

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
