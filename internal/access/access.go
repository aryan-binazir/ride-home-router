// Package access owns Clerk authentication and shared application admission.
package access

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
	"github.com/clerk/clerk-sdk-go/v2/session"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

// Config must be identical on every instance. No authentication bypass exists.
type Config struct {
	// HTTPClient optionally supplies the Clerk API transport for integration tests.
	// Production leaves this nil to use the bounded HTTPS client.
	HTTPClient        *http.Client
	SecretKey         string
	PublishableKey    string
	JWTKey            string
	AuthorizedParties string
	AdminEmails       string
}

// Store persists admission separately from Clerk identity and administrator status.
type Store interface {
	Approved(context.Context, []string) (bool, error)
	ApprovedEmails(context.Context) ([]string, error)
	AddApprovedEmail(context.Context, string) error
	RemoveApprovedEmail(context.Context, string) error
}

type Access struct {
	cfg      Config
	issuer   string
	key      *clerk.JSONWebKey
	parties  map[string]bool
	admins   map[string]bool
	users    *user.Client
	sessions *session.Client
	store    Store
}

type principalKey struct{}

// IsAdmin only consumes identity installed by this package after verification.
func IsAdmin(ctx context.Context) bool { admin, _ := ctx.Value(principalKey{}).(bool); return admin }

func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || !strings.Contains(email, "@") || len(email) > 254 {
		return "", errors.New("enter a valid email address")
	}
	return email, nil
}

func New(cfg Config, store Store) (*Access, error) {
	if cfg.SecretKey == "" || cfg.PublishableKey == "" || cfg.JWTKey == "" || store == nil {
		return nil, errors.New("CLERK_SECRET_KEY, CLERK_PUBLISHABLE_KEY and CLERK_JWT_KEY are required")
	}
	encoded := strings.TrimPrefix(strings.TrimPrefix(cfg.PublishableKey, "pk_test_"), "pk_live_")
	if encoded == cfg.PublishableKey {
		return nil, errors.New("invalid CLERK_PUBLISHABLE_KEY")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return nil, errors.New("invalid CLERK_PUBLISHABLE_KEY")
	}
	host := strings.TrimSuffix(string(decoded), "$")
	u, err := url.Parse("https://" + host)
	if err != nil || u.Hostname() != host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !strings.Contains(host, ".") {
		return nil, errors.New("invalid Clerk frontend hostname")
	}
	block, _ := pem.Decode([]byte(strings.ReplaceAll(cfg.JWTKey, `\n`, "\n")))
	if block == nil {
		return nil, errors.New("CLERK_JWT_KEY must be a PEM public key")
	}
	public, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid CLERK_JWT_KEY")
	}
	rsaKey, ok := public.(*rsa.PublicKey)
	if !ok || rsaKey.N.BitLen() < 2048 {
		return nil, errors.New("CLERK_JWT_KEY must be an RSA public key of at least 2048 bits")
	}
	a := &Access{cfg: cfg, issuer: "https://" + host, key: &clerk.JSONWebKey{Key: rsaKey, Algorithm: "RS256"}, parties: map[string]bool{}, admins: map[string]bool{}, store: store}
	for raw := range strings.SplitSeq(cfg.AuthorizedParties, ",") {
		origin := strings.TrimSpace(raw)
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1"))) {
			return nil, errors.New("CLERK_AUTHORIZED_PARTIES must contain exact HTTPS origins (HTTP only on loopback)")
		}
		a.parties[origin] = true
	}
	for raw := range strings.SplitSeq(cfg.AdminEmails, ",") {
		email, err := NormalizeEmail(raw)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_EMAILS: %w", err)
		}
		a.admins[email] = true
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	client := &clerk.ClientConfig{Key: new(cfg.SecretKey), HTTPClient: httpClient}
	a.users = user.NewClient(client)
	a.sessions = session.NewClient(client)
	return a, nil
}

func token(r *http.Request) string {
	if values, ok := r.Header["Authorization"]; ok {
		if len(values) != 1 {
			return ""
		}
		fields := strings.Fields(values[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			return ""
		}
		return fields[1]
	}
	cookies := r.CookiesNamed("__session")
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}

func public(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/sign-in", "/auth/config", "/api/v1/health", "/api/v1/ready":
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/static/") && !strings.Contains(r.URL.Path, "..")
}

// Protect is the outer boundary around the entire router, including future routes.
func (a *Access) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if public(r) {
			next.ServeHTTP(w, r)
			return
		}
		raw := token(r)
		if raw == "" || len(raw) > 16384 {
			deny(w, r, http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		state := struct {
			Status string `json:"sts"`
		}{}
		claims, err := jwt.Verify(ctx, &jwt.VerifyParams{Token: raw, JWK: a.key, CustomClaimsConstructor: func(context.Context) any { return &state }, AuthorizedPartyHandler: func(p string) bool { return a.parties[p] }})
		if err != nil || (state.Status != "" && state.Status != "active") || claims.Issuer != a.issuer || claims.Subject == "" || claims.SessionID == "" || claims.Expiry == nil || claims.NotBefore == nil || claims.IssuedAt == nil || *claims.Expiry <= time.Now().Unix() {
			deny(w, r, http.StatusUnauthorized)
			return
		}
		// Live lookups also reject revoked sessions and removed/unverified addresses.
		s, err := a.sessions.Get(ctx, claims.SessionID)
		if err != nil || s.ID != claims.SessionID || s.UserID != claims.Subject || s.Status != "active" {
			deny(w, r, http.StatusUnauthorized)
			return
		}
		u, err := a.users.Get(ctx, claims.Subject)
		if err != nil || u.ID != claims.Subject || u.Banned || u.Locked {
			deny(w, r, http.StatusUnauthorized)
			return
		}
		emails := []string{}
		admin := false
		for _, e := range u.EmailAddresses {
			if e == nil || e.Verification == nil || e.Verification.Status != "verified" {
				continue
			}
			email, err := NormalizeEmail(e.EmailAddress)
			if err != nil {
				continue
			}
			emails = append(emails, email)
			admin = admin || a.admins[email]
		}
		if !admin {
			allowed, err := a.store.Approved(ctx, emails)
			if err != nil {
				deny(w, r, http.StatusServiceUnavailable)
				return
			}
			if !allowed {
				deny(w, r, http.StatusForbidden)
				return
			}
		}
		// Cookie-authenticated writes require an explicit matching origin. Bearer
		// clients need no CSRF token; browsers cannot add this header cross-origin.
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.Header.Get("Authorization") == "" {
			origin := r.Header.Get("Origin")
			u, err := url.Parse(origin)
			if err != nil || !a.parties[origin] || u.Host != r.Host {
				deny(w, r, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, admin)))
	})
}

func deny(w http.ResponseWriter, r *http.Request, status int) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Reswap", "none")
		if status == http.StatusUnauthorized {
			w.Header().Set("HX-Redirect", "/sign-in")
		}
		if status == http.StatusForbidden {
			w.Header().Set("HX-Redirect", "/sign-in?denied=1")
		}
	} else if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") && !strings.HasPrefix(r.URL.Path, "/api/") && status != http.StatusServiceUnavailable {
		target := "/sign-in"
		if status == http.StatusForbidden {
			target += "?denied=1"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Error(w, http.StatusText(status), status)
}
