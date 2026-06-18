package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nonceKey struct{}

type rateLimiter struct {
	mu     sync.Mutex
	window time.Duration
	limit  int
	visits map[string][]time.Time
}

func newRateLimiter(window time.Duration, limit int) *rateLimiter {
	if window < time.Second {
		window = time.Second
	}
	if limit < 1 {
		limit = 1
	}
	return &rateLimiter{
		window: window,
		limit:  limit,
		visits: make(map[string][]time.Time),
	}
}

func (l *rateLimiter) allow(ip string) (bool, time.Duration) {
	now := time.Now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	visits := l.visits[ip]
	kept := visits[:0]
	for _, t := range visits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	visits = kept
	if len(visits) >= l.limit {
		l.visits[ip] = visits
		return false, visits[0].Add(l.window).Sub(now)
	}

	if len(visits) == 0 {
		delete(l.visits, ip)
	}

	visits = append(visits, now)
	l.visits[ip] = visits
	return true, 0
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

func nonceFrom(ctx context.Context) string {
	if v, ok := ctx.Value(nonceKey{}).(string); ok {
		return v
	}
	return ""
}

func clientIP(r *http.Request, trustProxy bool) string {
	if !trustProxy {
		return remoteIP(r)
	}
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); isValidIP(ip) {
		return ip
	}
	if ip := firstForwardedIP(r.Header.Get("X-Forwarded-For")); isValidIP(ip) {
		return ip
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); isValidIP(ip) {
		return ip
	}
	return remoteIP(r)
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func firstForwardedIP(header string) string {
	for _, part := range strings.Split(header, ",") {
		ip := strings.TrimSpace(part)
		if isValidIP(ip) {
			return ip
		}
	}
	return ""
}

func isValidIP(s string) bool {
	return net.ParseIP(s) != nil
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.RateLimitEnabled {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r, s.cfg.TrustProxy)
		if ok, retry := s.limiter.allow(ip); !ok {
			if retry < time.Second {
				retry = time.Second
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// securityHeaders applies a strict Content-Security-Policy that forbids all
// third-party resources (no external CSS/JS/fonts/images). The only inline
// script permitted is the JSON-LD block, allowed via a per-request nonce.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := newNonce()
		csp := strings.Join([]string{
			"default-src 'none'",
			"img-src 'self' data:",
			"style-src 'self'",
			"script-src 'nonce-" + n + "'",
			"font-src 'self'",
			"connect-src 'self'",
			"base-uri 'self'",
			"form-action 'self'",
			"frame-ancestors 'none'",
		}, "; ")
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		ctx := context.WithValue(r.Context(), nonceKey{}, n)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
