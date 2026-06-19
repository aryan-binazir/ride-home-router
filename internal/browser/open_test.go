package browser

import (
	"errors"
	"testing"
)

func TestValidateURL_acceptsHTTPURLs(t *testing.T) {
	tests := []string{
		"http://127.0.0.1:8080/",
		"http://localhost/",
		"https://www.google.com/maps/dir/?api=1",
	}
	for _, raw := range tests {
		if _, err := ValidateURL(raw); err != nil {
			t.Fatalf("ValidateURL(%q) error = %v, want nil", raw, err)
		}
	}
}

func TestValidateURL_rejectsInvalidURLs(t *testing.T) {
	tests := []string{
		"",
		"javascript:alert(1)",
		"http://",
		"https:///path",
		"ftp://example.com",
		"http://user:pass@127.0.0.1/",
		"http://127.0.0.1/\n",
	}
	for _, raw := range tests {
		if _, err := ValidateURL(raw); !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("ValidateURL(%q) error = %v, want ErrInvalidURL", raw, err)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	got, err := NormalizeURL("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("NormalizeURL() error = %v", err)
	}
	if got != "http://127.0.0.1:8080" {
		t.Fatalf("NormalizeURL() = %q, want canonical URL", got)
	}
}
