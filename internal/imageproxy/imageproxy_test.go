package imageproxy

import (
	"net/url"
	"testing"
)

func TestValidateURLRejectsInsecureSchemes(t *testing.T) {
	u := mustParseURL(t, "http://example.com/image.png")

	if err := validateURL(u); err == nil {
		t.Fatal("validateURL accepted http URL")
	}
}

func TestValidateURLRejectsLoopback(t *testing.T) {
	u := mustParseURL(t, "https://127.0.0.1/image.png")

	if err := validateURL(u); err == nil {
		t.Fatal("validateURL accepted loopback URL")
	}
}

func TestValidateURLRejectsPrivateAddress(t *testing.T) {
	u := mustParseURL(t, "https://10.0.0.1/image.png")

	if err := validateURL(u); err == nil {
		t.Fatal("validateURL accepted private URL")
	}
}

func TestValidateURLRejectsEmptyHost(t *testing.T) {
	u := mustParseURL(t, "https:///image.png")

	if err := validateURL(u); err == nil {
		t.Fatal("validateURL accepted empty host")
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
