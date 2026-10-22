package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nonceKey struct{}

// maxTrackedIPs caps the limiter's memory. Once reached (after sweeping idle
// entries), requests from IPs not already tracked are rejected.
const maxTrackedIPs = 100_000

type rateLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	limit     int
	visits    map[string][]time.Time
	lastSweep time.Time
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

	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(cutoff)
		l.lastSweep = now
	}

	visits, tracked := l.visits[ip]
	if !tracked && len(l.visits) >= maxTrackedIPs {
		return false, l.window
	}
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

	visits = append(visits, now)
	l.visits[ip] = visits
	return true, 0
}

// sweep drops IPs with no visits inside the window. Caller holds l.mu.
func (l *rateLimiter) sweep(cutoff time.Time) {
	for ip, visits := range l.visits {
		if len(visits) == 0 || !visits[len(visits)-1].After(cutoff) {
			delete(l.visits, ip)
		}
	}
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

// clientIP returns the address used for rate limiting. Forwarding headers are
// only honored when trustProxy is set and the direct peer is a trusted proxy;
// otherwise any client could spoof its IP.
func clientIP(r *http.Request, trustProxy bool, trusted []netip.Prefix) string {
	peer := remoteIP(r)
	if !trustProxy || !inPrefixes(peer, trusted) {
		return peer
	}
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); isValidIP(ip) {
		return ip
	}
	if ip := forwardedClientIP(r.Header.Get("X-Forwarded-For"), trusted); ip != "" {
		return ip
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); isValidIP(ip) {
		return ip
	}
	return peer
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// forwardedClientIP walks X-Forwarded-For from the right and returns the first
// hop that is not a trusted proxy. Left-most entries are client-controlled, so
// they are only used if every hop to their right is trusted.
func forwardedClientIP(header string, trusted []netip.Prefix) string {
	parts := strings.Split(header, ",")
	last := ""
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if !isValidIP(ip) {
			break
		}
		last = ip
		if !inPrefixes(ip, trusted) {
			return ip
		}
	}
	return last
}

func inPrefixes(ip string, prefixes []netip.Prefix) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
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

		ip := clientIP(r, s.cfg.TrustProxy, s.cfg.TrustedProxies)
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
// CSP is only applied to HTML pages to avoid interfering with native browser
// stylesheets on XML/JSON/plain-text responses.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		isHTML := !strings.HasPrefix(path, "/static/") &&
			path != "/img" &&
			path != "/robots.txt" &&
			path != "/sitemap.xml" &&
			path != "/healthz"

		h := w.Header()
		if isHTML {
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
			h.Set("Content-Security-Policy", csp)
			ctx := context.WithValue(r.Context(), nonceKey{}, n)
			r = r.WithContext(ctx)
		}

		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		next.ServeHTTP(w, r)
	})
}
