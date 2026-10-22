package imageproxy

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

// validateURLFast checks scheme and host only; IP validation is in the dialer.

func TestValidateURLFastRejectsHTTP(t *testing.T) {
	u := mustParseURL(t, "http://example.com/image.png")
	if err := validateURLFast(u); err == nil {
		t.Fatal("validateURLFast accepted http URL")
	}
}

func TestValidateURLFastRejectsFTP(t *testing.T) {
	u := mustParseURL(t, "ftp://example.com/image.png")
	if err := validateURLFast(u); err == nil {
		t.Fatal("validateURLFast accepted ftp URL")
	}
}

func TestValidateURLFastRejectsEmptyHost(t *testing.T) {
	u := mustParseURL(t, "https:///image.png")
	if err := validateURLFast(u); err == nil {
		t.Fatal("validateURLFast accepted empty host")
	}
}

func TestValidateURLFastAcceptsHTTPS(t *testing.T) {
	u := mustParseURL(t, "https://example.com/image.png")
	if err := validateURLFast(u); err != nil {
		t.Fatalf("validateURLFast rejected valid HTTPS URL: %v", err)
	}
}

// IP-level SSRF blocking is now enforced by the custom DialContext.
// The loopback/private tests are intentionally removed from validateURLFast
// because IP validation no longer happens there — it happens at dial-time,
// which prevents the DNS rebinding attack window.

func TestIsBlockedIP(t *testing.T) {
	for _, ip := range []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "198.18.0.1", "224.0.0.1", "::1", "fe80::1",
		"fd00::1", "64:ff9b::a00:1", "::ffff:127.0.0.1", "2002:a00:1::",
	} {
		if !isBlockedIP(netip.MustParseAddr(ip)) {
			t.Errorf("isBlockedIP(%s) = false, want true", ip)
		}
	}
	for _, ip := range []string{"140.82.112.3", "185.199.108.133", "2606:50c0:8000::153"} {
		if isBlockedIP(netip.MustParseAddr(ip)) {
			t.Errorf("isBlockedIP(%s) = true, want false", ip)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	ok := map[string]string{
		"image/png":                    "image/png",
		"IMAGE/JPEG; charset=binary":   "image/jpeg",
		"image/svg+xml; charset=utf-8": "image/svg+xml",
	}
	for in, want := range ok {
		got, err := normalizeType(in)
		if err != nil || got != want {
			t.Errorf("normalizeType(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"text/html; x=svg", "text/html", "application/xml", "", "image/x-unknown"} {
		if got, err := normalizeType(in); err == nil {
			t.Errorf("normalizeType(%q) = %q, want error", in, got)
		}
	}
}

func TestServeHTTPRejectsUnsignedAndBadSignature(t *testing.T) {
	h := New(nil, 0, []byte("secret"))
	for _, target := range []string{
		"/img?u=" + url.QueryEscape("https://example.com/x.png"),
		"/img?u=" + url.QueryEscape("https://example.com/x.png") + "&s=deadbeef",
		// Signature for a different URL must not validate.
		"/img?u=" + url.QueryEscape("https://example.com/b.png") + "&s=" + h.sign("https://example.com/a.png"),
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		if rr.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", target, rr.Code)
		}
	}
}

func TestSignURLValidates(t *testing.T) {
	h := New(nil, 0, []byte("secret"))
	raw := "https://img.shields.io/badge/a-b-green.svg?x=1&y=2"
	r := httptest.NewRequest(http.MethodGet, h.SignURL(raw), nil)
	q := r.URL.Query()
	if q.Get("u") != raw || !h.validSignature(q.Get("u"), q.Get("s")) {
		t.Fatalf("SignURL(%q) produced an invalid URL: %s", raw, r.URL)
	}
	if New(nil, 0, []byte("other")).validSignature(raw, q.Get("s")) {
		t.Fatal("signature validated under a different key")
	}
}

func TestSignHTML(t *testing.T) {
	h := New(nil, 0, []byte("secret"))
	raw := "https://example.com/a b.png"
	in := `<p><img src="/img?u=` + url.QueryEscape(raw) + `" alt="x"></p>`
	out := h.SignHTML(in)
	want := `src="/img?u=` + url.QueryEscape(raw) + `&amp;s=` + h.sign(raw) + `"`
	if !strings.Contains(out, want) {
		t.Fatalf("SignHTML output = %s, want it to contain %s", out, want)
	}
}

func TestWriteImageHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	writeImage(rr, cachedImage{contentType: "image/svg+xml; charset=utf-8", data: []byte("<svg/>")}, false)
	if got := rr.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", got)
	}
	if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want sandbox", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got == "" {
		t.Error("Content-Disposition missing")
	}

	rr = httptest.NewRecorder()
	writeImage(rr, cachedImage{contentType: "text/html", data: []byte("<script>")}, true)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("cached text/html served with %d, want 502", rr.Code)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) failed: %v", raw, err)
	}
	return u
}
