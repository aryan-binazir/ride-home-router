// Package accesstest supplies synthetic Clerk identities without bypassing JWT verification.
package accesstest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"ride-home-router/internal/access"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v3"
)

const (
	Issuer = "https://fixture.clerk.accounts.dev"
	Origin = "http://127.0.0.1"
)

// Fixture owns a signing key and mutable, concurrency-safe Clerk API state.
type Fixture struct {
	t        testing.TB
	key      *rsa.PrivateKey
	mu       sync.Mutex
	users    map[string]any
	sessions map[string]any
	failures map[string]int
	public   string
	hostname string
	secret   string
}

// New creates an isolated RSA key. Config accepts any number of admin emails.
func New(t testing.TB) *Fixture {
	t.Helper()
	f := NewInstance(t, "fixture.clerk.accounts.dev")
	f.secret = "sk_test_synthetic"
	return f
}

// NewInstance creates a Clerk instance with its own issuer, RSA key and API secret.
// hostname is the bare Clerk frontend hostname, without a scheme or path.
func NewInstance(t testing.TB, hostname string) *Fixture {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return &Fixture{t: t, key: key, hostname: hostname, secret: "sk_test_" + base64.RawURLEncoding.EncodeToString(secret), public: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), users: map[string]any{}, sessions: map[string]any{}, failures: map[string]int{}}
}

func (f *Fixture) Config(adminEmails ...string) access.Config {
	if len(adminEmails) == 0 {
		adminEmails = []string{"admin@example.test"}
	}
	//nolint:gosec // Synthetic credential accepted only by this in-memory transport.
	return access.Config{SecretKey: f.secret, PublishableKey: "pk_test_" + base64.RawStdEncoding.EncodeToString([]byte(f.hostname+"$")), JWTKey: f.public, AuthorizedParties: Origin + ",http://localhost:8080", AdminEmails: strings.Join(adminEmails, ","), HTTPClient: &http.Client{Transport: f, Timeout: time.Second}}
}

// User replaces an identity; verified and unverified addresses remain distinct.
func (f *Fixture) User(id string, verified, unverified []string) {
	emails := []any{}
	for _, group := range []struct {
		addresses []string
		status    string
	}{{verified, "verified"}, {unverified, "unverified"}} {
		for i, email := range group.addresses {
			emails = append(emails, map[string]any{"id": fmt.Sprintf("email_%s_%d", group.status, i), "email_address": email, "verification": map[string]string{"status": group.status}})
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[id] = map[string]any{"id": id, "object": "user", "email_addresses": emails}
}

// Session replaces a session, allowing revoked, expired, or mismatched identities.
func (f *Fixture) Session(id, userID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[id] = map[string]any{"id": id, "object": "session", "user_id": userID, "status": status}
}

// Fail sets an HTTP failure for an exact /v1/users/id or /v1/sessions/id path; zero clears it.
func (f *Fixture) Fail(path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[path] = status
}

// Token signs valid default claims. Overrides can replace or remove (nil) any claim.
// Token does not mutate user/session state, so a revoked session stays revoked.
func (f *Fixture) Token(userID, sessionID string, overrides ...map[string]any) string {
	f.t.Helper()
	now := time.Now().Unix()
	claims := map[string]any{"iss": "https://" + f.hostname, "sub": userID, "sid": sessionID, "azp": Origin, "iat": now - 10, "nbf": now - 10, "exp": now + 3600}
	for _, values := range overrides {
		for k, v := range values {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return token
}

// Admin provisions a default verified administrator and returns its signed token.
func (f *Fixture) Admin() string {
	f.User("user_admin", []string{"admin@example.test"}, nil)
	f.Session("sess_admin", "user_admin", "active")
	return f.Token("user_admin", "sess_admin")
}

// RoundTrip handles only bounded synthetic Clerk GET requests; it never uses the network.
func (f *Fixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.clerk.com" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+f.secret {
		return nil, fmt.Errorf("unexpected synthetic Clerk request: %s %s", r.Method, r.URL.Redacted())
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var value any
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/users/"):
		value = f.users[strings.TrimPrefix(r.URL.Path, "/v1/users/")]
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		value = f.sessions[strings.TrimPrefix(r.URL.Path, "/v1/sessions/")]
	default:
		return nil, fmt.Errorf("unexpected Clerk path %s", r.URL.Path)
	}
	status := http.StatusOK
	if value == nil {
		status = http.StatusNotFound
	}
	if failure := f.failures[r.URL.Path]; failure != 0 {
		status = failure
	}
	if status != http.StatusOK {
		value = map[string]any{"errors": []any{map[string]string{"code": "fixture_failure", "message": "synthetic failure"}}}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
}

// BearerTransport authenticates application requests while preserving cookies and redirects.
type BearerTransport struct {
	Token string
	Base  http.RoundTripper
}

func (b BearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.Token)
	base := b.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}
