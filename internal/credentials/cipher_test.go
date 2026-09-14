package credentials

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCipherAuthenticatesAndRandomizes(t *testing.T) {
	c, err := New(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := c.Seal("provider-secret", "google_maps_api_key")
	second, _ := c.Seal("provider-secret", "google_maps_api_key")
	if first == second || strings.Contains(first, "provider-secret") {
		t.Fatal("ciphertext reused a nonce or exposed plaintext")
	}
	got, err := c.Open(first, "google_maps_api_key")
	if err != nil || got != "provider-secret" {
		t.Fatal("round trip failed")
	}
	wrong, _ := New(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))))
	if _, err = wrong.Open(first, "google_maps_api_key"); err == nil {
		t.Fatal("wrong key accepted")
	}
	if _, err = c.Open(first, "another_credential"); err == nil {
		t.Fatal("wrong purpose accepted")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(first, prefix))
	raw[len(raw)-1] ^= 1
	for _, value := range []string{"provider-secret", "v2:" + first, "v1:!", "v1:", prefix + base64.StdEncoding.EncodeToString(raw)} {
		if _, err = c.Open(value, "google_maps_api_key"); err == nil {
			t.Fatal("invalid ciphertext accepted")
		}
	}
}

func TestCipherRequiresStrongKey(t *testing.T) {
	for _, key := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := New(key); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	var c *Cipher
	if _, err := c.Seal("secret", "google_maps_api_key"); err == nil {
		t.Fatal("plaintext fallback")
	}
	if _, err := c.Open("secret", "google_maps_api_key"); err == nil {
		t.Fatal("plaintext read fallback")
	}
}
