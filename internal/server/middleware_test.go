package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"gitindex/internal/config"
)

var testTrusted = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
}

func TestClientIPProxyHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.10:45678"
	r.Header.Set("CF-Connecting-IP", "203.0.113.10")
	r.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.20")
	r.Header.Set("X-Real-IP", "192.0.2.30")

	if got := clientIP(r, true, testTrusted); got != "203.0.113.10" {
		t.Fatalf("clientIP with CF header = %q, want 203.0.113.10", got)
	}

	r.Header.Del("CF-Connecting-IP")
	if got := clientIP(r, true, testTrusted); got != "198.51.100.20" {
		t.Fatalf("clientIP with X-Forwarded-For = %q, want 198.51.100.20", got)
	}

	r.Header.Del("X-Forwarded-For")
	if got := clientIP(r, true, testTrusted); got != "192.0.2.30" {
		t.Fatalf("clientIP with X-Real-IP = %q, want 192.0.2.30", got)
	}

	if got := clientIP(r, false, testTrusted); got != "10.0.0.10" {
		t.Fatalf("clientIP with trustProxy=false = %q, want 10.0.0.10", got)
	}
}

// Headers from an untrusted (directly connecting) peer must be ignored.
func TestClientIPIgnoresHeadersFromUntrustedPeer(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "198.51.100.99:1234"
	r.Header.Set("CF-Connecting-IP", "203.0.113.10")
	r.Header.Set("X-Forwarded-For", "203.0.113.11")
	r.Header.Set("X-Real-IP", "203.0.113.12")

	if got := clientIP(r, true, testTrusted); got != "198.51.100.99" {
		t.Fatalf("clientIP from untrusted peer = %q, want 198.51.100.99", got)
	}
}

// The right-most untrusted X-Forwarded-For hop wins; spoofed left entries don't.
func TestClientIPRightmostUntrustedXFF(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.10:45678"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.20, 10.0.0.20")

	if got := clientIP(r, true, testTrusted); got != "198.51.100.20" {
		t.Fatalf("clientIP = %q, want 198.51.100.20", got)
	}
}

func TestRateLimiterSweepsIdleEntries(t *testing.T) {
	limiter := newRateLimiter(time.Second, 5)
	limiter.allow("203.0.113.1")
	limiter.visits["203.0.113.1"][0] = time.Now().Add(-time.Hour)
	limiter.lastSweep = time.Now().Add(-time.Hour)

	limiter.allow("203.0.113.2")

	if _, ok := limiter.visits["203.0.113.1"]; ok {
		t.Fatal("idle IP was not swept")
	}
}

func TestRateLimiterCapsTrackedIPs(t *testing.T) {
	limiter := newRateLimiter(time.Minute, 5)
	now := time.Now()
	limiter.lastSweep = now
	for i := range maxTrackedIPs {
		limiter.visits[fmt.Sprintf("ip-%d", i)] = []time.Time{now}
	}

	if ok, _ := limiter.allow("203.0.113.1"); ok {
		t.Fatal("new IP allowed although the limiter is full")
	}
	if ok, _ := limiter.allow("ip-1"); !ok {
		t.Fatal("already tracked IP was denied")
	}
}

func TestRateLimiterAllowsAndDenies(t *testing.T) {
	limiter := newRateLimiter(time.Minute, 2)

	if ok, _ := limiter.allow("203.0.113.1"); !ok {
		t.Fatal("first request was denied")
	}
	if ok, _ := limiter.allow("203.0.113.1"); !ok {
		t.Fatal("second request was denied")
	}
	if ok, retry := limiter.allow("203.0.113.1"); ok {
		t.Fatalf("third request was allowed, want denied, retry=%s", retry)
	}
	if ok, _ := limiter.allow("203.0.113.2"); !ok {
		t.Fatal("different IP should have its own limit")
	}
}

func TestRateLimitMiddlewareReturns429(t *testing.T) {
	s := &Server{
		cfg: config.Config{
			RateLimitEnabled:  true,
			RateLimitWindow:   time.Minute,
			RateLimitRequests: 1,
			TrustProxy:        true,
			TrustedProxies:    testTrusted,
		},
		limiter: newRateLimiter(time.Minute, 1),
	}
	handler := s.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	first := httptest.NewRequest(http.MethodGet, "/", nil)
	first.RemoteAddr = "127.0.0.1:1234"
	first.Header.Set("CF-Connecting-IP", "203.0.113.50")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, first)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("first status = %d, want 204", rr.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/", nil)
	second.RemoteAddr = "127.0.0.1:1235"
	second.Header.Set("CF-Connecting-IP", "203.0.113.50")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, second)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Fatal("Retry-After header missing")
	}
}

func TestSecurityHeaders(t *testing.T) {
	s := &Server{}
	handler := s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	if got := rr.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
