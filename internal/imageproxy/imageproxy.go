// Package imageproxy serves README images from the local origin instead of
// third-party hosts, so rendered pages contact no external servers and a
// strict Content-Security-Policy (img-src 'self') can be enforced.
//
// Fetched images are cached in Redis. The fetcher is hardened against SSRF:
// https only, and any address resolving to a non-public IP (private, loopback,
// link-local, CGNAT, NAT64, documentation, ...) is refused at TCP dial-time
// (not just pre-flight) to prevent DNS rebinding.
//
// Proxy URLs are signed with an HMAC (see SignURL/SignHTML), so only images the
// site itself links to can be fetched; the proxy is not an open HTTPS fetcher.
// Only raster image types and SVG are served, with a normalized Content-Type
// and a sandboxing CSP so an SVG opened directly can never run script.
package imageproxy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gitindex/internal/cache"
)

const maxImageBytes = 12 << 20 // 12 MiB

// allowedTypes are the only media types the proxy will serve. Anything else
// (text/html, application/xml, ...) is rejected so the proxy can never serve an
// active document on the site origin.
var allowedTypes = map[string]bool{
	"image/png":                true,
	"image/jpeg":               true,
	"image/gif":                true,
	"image/webp":               true,
	"image/avif":               true,
	"image/bmp":                true,
	"image/x-icon":             true,
	"image/vnd.microsoft.icon": true,
	// SVG is common for README badges. It is served with a sandboxing CSP
	// (see writeImage), so scripts never run even if it is opened directly.
	"image/svg+xml": true,
}

// imageCSP neutralizes any active content in a proxied response that is
// opened as a top-level document (e.g. an SVG containing <script>).
const imageCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// blockedPrefixes are non-public address ranges the proxy must never dial.
// IPv4-mapped IPv6 addresses are unmapped before the check.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "this" network
		"10.0.0.0/8",      // RFC1918
		"100.64.0.0/10",   // CGNAT (RFC6598), e.g. Tailscale
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local / cloud metadata
		"172.16.0.0/12",   // RFC1918
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // TEST-NET-1
		"192.88.99.0/24",  // 6to4 relay anycast
		"192.168.0.0/16",  // RFC1918
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved + broadcast
		"::/128",          // unspecified
		"::1/128",         // loopback
		"64:ff9b::/96",    // NAT64 (can map to internal IPv4)
		"64:ff9b:1::/48",  // local-use NAT64
		"100::/64",        // discard-only
		"2001::/23",       // IETF protocol assignments (incl. Teredo)
		"2001:db8::/32",   // documentation
		"2002::/16",       // 6to4 (embeds IPv4)
		"fc00::/7",        // unique local
		"fe80::/10",       // link-local
		"ff00::/8",        // multicast
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// isBlockedIP reports whether ip is outside the public unicast address space.
func isBlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

type Handler struct {
	cache *cache.Cache
	ttl   time.Duration
	key   []byte
	http  *http.Client
}

// New returns the proxy handler. key is the HMAC secret used to sign and
// verify proxy URLs; requests are refused while it is empty.
func New(c *cache.Cache, ttl time.Duration, key []byte) *Handler {
	return &Handler{
		cache: c,
		ttl:   ttl,
		key:   key,
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
						ip, err := netip.ParseAddr(ipStr)
						if err != nil {
							continue
						}
						if isBlockedIP(ip) {
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
	if !h.validSignature(raw, r.URL.Query().Get("s")) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || validateURLFast(u) != nil {
		http.Error(w, "invalid or disallowed url", http.StatusBadRequest)
		return
	}

	// v2: entries store the normalized content-type, not the upstream header.
	key := "img:v2:" + hash(raw)
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
	ct, err := normalizeType(resp.Header.Get("Content-Type"))
	if err != nil {
		return cachedImage{}, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return cachedImage{}, err
	}
	return cachedImage{contentType: ct, data: data}, nil
}

func writeImage(w http.ResponseWriter, img cachedImage, cached bool) {
	ct, err := normalizeType(img.contentType)
	if err != nil {
		http.Error(w, "upstream image error", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Security-Policy", imageCSP)
	w.Header().Set("Content-Disposition", `inline; filename="img"`)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if cached {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}
	_, _ = w.Write(img.data)
}

// normalizeType parses an upstream Content-Type and returns the bare media
// type if it is on the allowlist. Parameters are dropped so nothing from the
// upstream header is reflected verbatim.
func normalizeType(header string) (string, error) {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		return "", fmt.Errorf("bad content-type %q: %w", header, err)
	}
	if !allowedTypes[mt] {
		return "", fmt.Errorf("content-type not allowed: %q", mt)
	}
	return mt, nil
}

// sign returns the hex HMAC-SHA256 of the raw image URL.
func (h *Handler) sign(raw string) string {
	m := hmac.New(sha256.New, h.key)
	_, _ = m.Write([]byte(raw))
	return hex.EncodeToString(m.Sum(nil))
}

func (h *Handler) validSignature(raw, sig string) bool {
	if len(h.key) == 0 || sig == "" {
		return false
	}
	return hmac.Equal([]byte(h.sign(raw)), []byte(sig))
}

// SignURL returns the signed, root-relative proxy path for an absolute image
// URL, e.g. "/img?u=https%3A%2F%2F...&s=<hmac>".
func (h *Handler) SignURL(raw string) string {
	return "/img?u=" + url.QueryEscape(raw) + "&s=" + h.sign(raw)
}

// proxySrcRe matches unsigned proxy URLs in sanitized HTML. The u value is
// query-escaped by render.proxify, so it never contains '"' or '&'.
var proxySrcRe = regexp.MustCompile(`"/img\?u=([^"&]+)"`)

// SignHTML appends a signature to every unsigned "/img?u=..." attribute value
// in already-sanitized HTML. Signing at response time (instead of at render
// time) keeps cached README HTML valid across restarts and secret changes.
func (h *Handler) SignHTML(s string) string {
	return proxySrcRe.ReplaceAllStringFunc(s, func(m string) string {
		esc := proxySrcRe.FindStringSubmatch(m)[1]
		raw, err := url.QueryUnescape(esc)
		if err != nil {
			return m
		}
		return `"/img?u=` + esc + `&amp;s=` + h.sign(raw) + `"`
	})
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
