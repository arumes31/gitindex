// Package imageproxy serves README images from the local origin instead of
// third-party hosts, so rendered pages contact no external servers and a
// strict Content-Security-Policy (img-src 'self') can be enforced.
//
// Fetched images are cached in Redis. The fetcher is hardened against SSRF:
// https only, and any URL resolving to a private/loopback/link-local address
// is refused.
package imageproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
			// Don't follow redirects to other hosts blindly — re-validate each hop.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 4 {
					return fmt.Errorf("too many redirects")
				}
				return validateURL(req.URL)
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
	if err != nil || validateURL(u) != nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return cachedImage{}, err
	}
	req.Header.Set("User-Agent", "gitindex-imageproxy")
	req.Header.Set("Accept", "image/*")
	resp, err := h.http.Do(req)
	if err != nil {
		return cachedImage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cachedImage{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") && !strings.Contains(ct, "svg") {
		return cachedImage{}, fmt.Errorf("not an image: %q", ct)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return cachedImage{}, err
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
	w.Write(img.data)
}

// validateURL enforces https and blocks private/loopback/link-local targets.
func validateURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("scheme not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed")
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("target address not allowed")
		}
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
