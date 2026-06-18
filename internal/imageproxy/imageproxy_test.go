package imageproxy

import (
	"net/url"
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

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) failed: %v", raw, err)
	}
	return u
}
