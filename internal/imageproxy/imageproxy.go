// Package imageproxy serves README images from the local origin instead of
// third-party hosts, so rendered pages contact no external servers and a
// strict Content-Security-Policy (img-src 'self') can be enforced.
//
// Fetched images are cached in Redis. The fetcher is hardened against SSRF:
// https only, and any address resolving to a private/loopback/link-local IP
// is refused at TCP dial-time (not just pre-flight) to prevent DNS rebinding.
package imageproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitindex/internal/cache"
)

const maxImageBytes = 12 << 20 // 12 MiB

type Handler struct {
	cache *cache.Cache
	ttl   time.Duration
	http  *http.Client
}

func New(c *cache.Cache, ttl time.Duration) *Handler {
	return &Handler{
		cache: c,
		ttl:   ttl,
		http: &http.Client{
			Timeout: 12 * time.Second,
			Transport: &http.Transport{
				// Validate the resolved IP at dial-time, not just before the
				// request. This closes the DNS rebinding window where a DNS
				// server returns a public IP during validateURLFast() but
				// rebinds to a private IP during the actual TCP connect.
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, fmt.Errorf("invalid addr %q: %w", addr, err)
					}
					ips, err := net.DefaultResolver.LookupHost(ctx, host)
					if err != nil {
						return nil, fmt.Errorf("dns lookup failed for %q: %w", host, err)
					}
					var validated []string
					for _, ipStr := range ips {
						ip := net.ParseIP(ipStr)
						if ip == nil {
							continue
						}
						if !ip.IsGlobalUnicast() || ip.IsPrivate() {
							return nil, fmt.Errorf("target address %s not allowed", ipStr)
						}
						validated = append(validated, ipStr)
					}
					if len(validated) == 0 {
						return nil, fmt.Errorf("no usable address for %q", host)
					}
					// Dial the validated IPs directly (no second DNS round-trip,
					// which could rebind), trying each in turn so an unreachable
					// address on a dual-stack host falls back to the next.
					dialer := &net.Dialer{Timeout: 10 * time.Second}
					var dialErr error
					for _, ipStr := range validated {
						conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipStr, port))
						if err == nil {
							return conn, nil
						}
						dialErr = err
					}
					return nil, dialErr
				},
			},
			// Re-validate each redirect hop at the URL level (scheme check).
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 4 {
					return fmt.Errorf("too many redirects")
				}
				return validateURLFast(req.URL)
			},
		},
	}
}

type cachedImage struct {
	contentType string
	data        []byte
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || validateURLFast(u) != nil {
		http.Error(w, "invalid or disallowed url", http.StatusBadRequest)
		return
	}

	key := "img:v1:" + hash(raw)
	if b, ok := h.cache.GetBytes(r.Context(), key); ok {
		if img, ok := decode(b); ok {
			writeImage(w, img, true)
			return
		}
	}

	img, err := h.fetch(r.Context(), u)
	if err != nil {
		http.Error(w, "upstream image error", http.StatusBadGateway)
		return
	}
	h.cache.SetBytes(r.Context(), key, encode(img), h.ttl)
	writeImage(w, img, false)
}

func (h *Handler) fetch(ctx context.Context, u *url.URL) (cachedImage, error) {
	// #nosec G704 -- u passed validateURLFast (https-only); resolved IPs are re-checked at dial-time
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return cachedImage{}, err
	}
	req.Header.Set("User-Agent", "gitindex-imageproxy")
	req.Header.Set("Accept", "image/*")
	// #nosec G704 -- request URL validated; transport DialContext re-validates resolved IPs (SSRF/rebind guard)
	resp, err := h.http.Do(req)
	if err != nil {
		return cachedImage{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return cachedImage{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(ct, "image/") {
		return cachedImage{}, fmt.Errorf("not an image: %q", resp.Header.Get("Content-Type"))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return cachedImage{}, err
	}
	if len(data) > maxImageBytes {
		return cachedImage{}, fmt.Errorf("image exceeds %d-byte limit", maxImageBytes)
	}
	return cachedImage{contentType: ct, data: data}, nil
}

func writeImage(w http.ResponseWriter, img cachedImage, cached bool) {
	w.Header().Set("Content-Type", img.contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if cached {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}
	_, _ = w.Write(img.data)
}

// validateURLFast performs a fast pre-flight check: https scheme only and
// non-empty host. IP-level SSRF validation is done at dial-time by the
// custom DialContext above, which closes the DNS rebinding attack window.
func validateURLFast(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("scheme not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	return nil
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// encode/decode store image bytes with their content-type in one blob.
func encode(img cachedImage) []byte {
	return append([]byte(img.contentType+"\n"), img.data...)
}

func decode(b []byte) (cachedImage, bool) {
	i := strings.IndexByte(string(b), '\n')
	if i < 0 {
		return cachedImage{}, false
	}
	return cachedImage{contentType: string(b[:i]), data: b[i+1:]}, true
}
