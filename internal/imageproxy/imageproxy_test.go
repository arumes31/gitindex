package imageproxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

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

func TestFetchRejectsMisleadingContentType(t *testing.T) {
	handler := &Handler{http: responseClient("text/plain; profile=svg", "not an image")}
	_, err := handler.fetch(context.Background(), mustParseURL(t, "https://example.com/image"))
	if err == nil {
		t.Fatal("fetch accepted a non-image media type containing svg")
	}
}

func TestFetchRejectsOversizedImage(t *testing.T) {
	handler := &Handler{http: responseClient("image/png", strings.Repeat("x", maxImageBytes+1))}
	_, err := handler.fetch(context.Background(), mustParseURL(t, "https://example.com/image.png"))
	if err == nil {
		t.Fatal("fetch accepted an image larger than the configured limit")
	}
}

func responseClient(contentType, body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
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
