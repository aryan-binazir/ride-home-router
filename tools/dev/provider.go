package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"ride-home-router/internal/access"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v3"
)

type provider struct {
	key                                          *rsa.PrivateKey
	origin, hostname, secret, public, capability string
	mu                                           sync.Mutex
	sessions                                     map[string]string
}

func randomID() string { return rand.Text() }

func newProvider(origin string) (*provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &provider{key: key, origin: origin, capability: randomID(), hostname: strings.ToLower(randomID()) + ".clerk.accounts.dev", secret: randomID(), public: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), sessions: map[string]string{}}, nil
}

func (p *provider) config(client *http.Client) access.Config {
	return access.Config{HTTPClient: client, SecretKey: p.secret, PublishableKey: "pk_test_" + base64.RawStdEncoding.EncodeToString([]byte(p.hostname+"$")), JWTKey: p.public, AuthorizedParties: p.origin, AdminEmails: "admin@example.test"}
}

func (p *provider) token(identity, sid string, expiry time.Time) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{"iss": "https://" + p.hostname, "sub": "user_" + identity, "sid": sid, "azp": p.origin, "iat": time.Now().Add(-time.Second).Unix(), "nbf": time.Now().Add(-time.Second).Unix(), "exp": expiry.Unix()})
	if err != nil {
		return "", err
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return signed.CompactSerialize()
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (p *provider) browser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	u, _ := url.Parse(p.origin)
	if r.Host != u.Host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != p.origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/__dev/session":
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Origin") != p.origin || r.Header.Get("X-Dev-Capability") != p.capability {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		var input struct {
			Identity string `json:"identity"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil {
			http.Error(w, "invalid identity", http.StatusBadRequest)
			return
		}
		switch input.Identity {
		case "admin", "member", "denied":
		default:
			http.Error(w, "invalid identity", http.StatusBadRequest)
			return
		}
		sid := randomID()
		raw, err := p.token(input.Identity, sid, time.Now().Add(time.Hour))
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		p.mu.Lock()
		p.sessions[sid] = input.Identity
		p.mu.Unlock()
		//nolint:gosec // Loopback HTTP cannot use Secure cookies; helpers enforce exact Host, Origin and capability.
		http.SetCookie(w, &http.Cookie{Name: "__session", Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
		writeJSON(w, map[string]string{"identity": input.Identity, "token": raw})
	case "/__dev/clerk.js":
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = fmt.Fprintf(w, "const devKey=%q;\n", p.capability)
		_, _ = w.Write([]byte(`window.Clerk={load:async()=>{},user:{primaryEmailAddress:{emailAddress:'synthetic local account'}},session:{getToken:async()=>{const r=await fetch('/__dev/token',{method:'POST',headers:{'X-Dev-Capability':devKey}});return r.ok?(await r.json()).token:null}},signOut:async()=>{location.href='/__dev/?choose=1&key='+devKey}};const banner=document.createElement('aside');banner.style='padding:8px;background:#fff0bf;color:#222;text-align:center';banner.textContent='Synthetic local data and travel estimates. ';const link=document.createElement('a');link.href='/__dev/?choose=1&key='+devKey;link.textContent='Switch identity';banner.append(link);document.body.prepend(banner);`))
	case "/__dev/token":
		if r.Method != http.MethodPost || r.Header.Get("Origin") != p.origin || r.Header.Get("X-Dev-Capability") != p.capability {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		cookie, err := r.Cookie("__session")
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		signed, err := jose.ParseSigned(cookie.Value)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		payload, err := signed.Verify(&p.key.PublicKey)
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		var claims struct {
			SID string `json:"sid"`
			Exp int64  `json:"exp"`
		}
		if json.Unmarshal(payload, &claims) != nil || claims.Exp <= time.Now().Unix() {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		p.mu.Lock()
		identity, ok := p.sessions[claims.SID]
		p.mu.Unlock()
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		raw, err := p.token(identity, claims.SID, time.Now().Add(time.Hour))
		if err != nil {
			http.Error(w, "signing failed", http.StatusInternalServerError)
			return
		}
		//nolint:gosec // Loopback HTTP cannot use Secure cookies; helpers enforce exact Host, Origin and capability.
		http.SetCookie(w, &http.Cookie{Name: "__session", Value: raw, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
		writeJSON(w, map[string]string{"token": raw})
	case "/__dev/login.js":
		if r.URL.Query().Get("key") != p.capability {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(`async function login(identity){const r=await fetch('/__dev/session',{method:'POST',headers:{'Content-Type':'application/json','X-Dev-Capability':new URL(location.href).searchParams.get('key')},body:JSON.stringify({identity})});if(!r.ok)throw Error('Local sign-in failed');location.href='/';}document.querySelectorAll('[data-identity]').forEach(b=>b.onclick=()=>login(b.dataset.identity));if(!new URL(location.href).searchParams.has('choose'))login('member');`))
	default:
		if r.URL.Query().Get("key") != p.capability {
			http.Error(w, "Open the login URL printed by make dev-status. Denied users can switch identities there.", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><title>Local identities</title><script defer src="/__dev/login.js?key=%s"></script></head><body><h1>Ride Home Router local environment</h1><p>Synthetic data only. Travel estimates are not real travel advice.</p><p>The denied identity has no application access.</p><button data-identity="admin">Admin</button> <button data-identity="member">Approved member</button> <button data-identity="denied">Denied user</button></body></html>`, p.capability)
	}
}

func (p *provider) api(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+p.secret || r.Method != http.MethodGet {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if sid, ok := strings.CutPrefix(r.URL.Path, "/v1/sessions/"); ok {
		identity, found := p.sessions[sid]
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]string{"id": sid, "object": "session", "user_id": "user_" + identity, "status": "active"})
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/v1/users/user_"); ok && (id == "admin" || id == "member" || id == "denied") {
		writeJSON(w, map[string]any{"id": "user_" + id, "object": "user", "email_addresses": []any{map[string]any{"id": "email_" + id, "email_address": id + "@example.test", "verification": map[string]string{"status": "verified"}}}})
		return
	}
	http.NotFound(w, r)
}

type localTransport struct {
	endpoint string
	base     *http.Transport
}

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		return nil, fmt.Errorf("local provider rejects non-HTTPS upstream")
	}
	allowed := false
	switch r.URL.Host {
	case "api.clerk.com":
		allowed = r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/v1/users/") || strings.HasPrefix(r.URL.Path, "/v1/sessions/")) && r.URL.RawQuery == ""
	case "maps.googleapis.com":
		allowed = r.Method == http.MethodGet && r.URL.Path == "/maps/api/geocode/json"
	case "places.googleapis.com":
		allowed = r.Method == http.MethodPost && r.URL.Path == "/v1/places:autocomplete"
	case "routes.googleapis.com":
		allowed = r.Method == http.MethodPost && r.URL.Path == "/directions/v2:computeRoutes"
	}
	if !allowed {
		return nil, fmt.Errorf("local provider rejects upstream request")
	}
	clone := r.Clone(r.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = t.endpoint
	clone.Host = t.endpoint
	clone.Header.Set("X-Local-Provider", r.URL.Host)
	return t.base.RoundTrip(clone)
}
