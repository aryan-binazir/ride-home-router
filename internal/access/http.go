package access

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"slices"

	"ride-home-router/internal/logutil"
	"ride-home-router/web"
)

// Register adds authentication and admin-only access management to the router.
// The entire router must still be wrapped with Protect.
func (a *Access) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /sign-in", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="color-scheme" content="light dark">
<title>Sign in - Ride Home Router</title>
<link rel="icon" type="image/svg+xml" href="` + web.AssetURL("img/favicon.svg") + `">
<link rel="stylesheet" href="` + web.AssetURL("css/style.css") + `">
<link rel="stylesheet" href="` + web.AssetURL("css/login.css") + `">
<script src="` + web.AssetURL("js/auth.js") + `" defer></script>
</head>
<body class="login-page">
<main class="login-shell">
<header class="login-brand">
<span class="login-mark" aria-hidden="true"><svg viewBox="0 0 32 32" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 23V12a5 5 0 0 1 10 0v8a5 5 0 0 0 10 0v-9"/><circle cx="6" cy="25" r="3" fill="currentColor" stroke="none"/><path d="m22 11 4-4 4 4"/></svg></span>
<h1>Ride Home Router</h1>

</header>
<section class="login-panel" aria-label="Sign in">
<h2 id="auth-heading" class="login-status-heading" hidden>Approval needed</h2>
<p id="auth-email" class="login-email" hidden></p>
<p id="auth-status" class="login-message" role="status" hidden></p>
<div id="clerk-sign-in"></div>
<button id="auth-retry" class="btn login-sign-out" type="button" hidden>Try again</button>
<button class="btn login-sign-out" type="button" data-sign-out hidden>Use another account</button>
<noscript><p class="login-message">Enable JavaScript to sign in.</p></noscript>
</section>

</main>
</body>
</html>`))
	})
	mux.HandleFunc("GET /auth/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"publishableKey": a.cfg.PublishableKey, "scriptURL": a.issuer + "/npm/@clerk/clerk-js@5/dist/clerk.browser.js"})
	})
	mux.HandleFunc("/api/v1/access", a.manage)
	mux.HandleFunc("GET /api/v1/access/admins", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !IsAdmin(r.Context()) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		emails := make([]string, 0, len(a.admins))
		for email := range a.admins {
			emails = append(emails, email)
		}
		slices.Sort(emails)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = adminsPanel.Execute(w, emails)
	})
}

var adminsPanel = template.Must(template.New("admins").Parse(`<section id="administrators" class="card">
<h2>Admins</h2>
<ul class="access-email-list">{{range .}}<li><span class="access-email">{{.}}</span><span class="badge badge-muted">Admin</span></li>{{end}}</ul>
</section>`))

var accessPanel = template.Must(template.New("access").Parse(`<section id="access-management" class="card">
<h2>Approved emails</h2>
<p>Approved accounts share all saved people, locations and events.</p>
{{if .Message}}<p role="status">{{.Message}}</p>{{end}}
<form class="access-approval-form" hx-sync="this:drop" hx-disabled-elt="find button[type=submit]" hx-post="/api/v1/access" hx-target="#access-management" hx-swap="outerHTML"><label for="approved-email">Email address</label><input id="approved-email" class="form-input" type="email" name="email" required maxlength="254"><button class="btn btn-primary" type="submit">Approve email</button></form>
<ul class="access-email-list">{{range .Emails}}<li><span class="access-email">{{.}}</span><form hx-confirm="Remove access for {{.}}?" hx-sync="this:drop" hx-disabled-elt="find button[type=submit]" hx-delete="/api/v1/access" hx-target="#access-management" hx-swap="outerHTML"><input type="hidden" name="email" value="{{.}}"><button class="btn" type="submit">Remove access</button></form></li>{{else}}<li>No approved emails yet. Add one above.</li>{{end}}</ul>
</section>`))

func (a *Access) manage(w http.ResponseWriter, r *http.Request) {
	reject := func(message string, status int) {
		if r.Header.Get("HX-Request") == "true" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-RHR-Access-Panel", "error")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`<section id="access-management" class="card"><h2>Approved emails</h2><p role="alert">Could not load approved emails. Try again.</p><button type="button" class="btn" hx-get="/api/v1/access" hx-target="#access-management" hx-swap="outerHTML" hx-disabled-elt="this">Try again</button></section>`))
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			payload, _ := json.Marshal(map[string]any{"showToast": map[string]string{"message": message, "type": "error"}})
			w.Header().Set("HX-Trigger", string(payload))
			w.Header().Set("HX-Reswap", "none")
		}
		http.Error(w, message, status)
	}
	if !IsAdmin(r.Context()) {
		log.Print("[AUTH] denied: non-admin access management")
		reject("Forbidden", http.StatusForbidden)
		return
	}
	message := ""
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost, http.MethodDelete:
		var input struct {
			Email string `json:"email"`
		}
		if r.Header.Get("HX-Request") == "true" {
			if err := r.ParseForm(); err != nil {
				reject("Invalid request", http.StatusBadRequest)
				return
			}
			input.Email = r.Form.Get("email")
		} else {
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				reject("Invalid request", http.StatusBadRequest)
				return
			}
		}
		email, err := NormalizeEmail(input.Email)
		if err != nil {
			reject("Invalid email address", http.StatusBadRequest)
			return
		}
		if a.admins[email] {
			reject("This address is an administrator and cannot be changed here.", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost {
			err = a.store.AddApprovedEmail(r.Context(), email)
			message = "Access approved."
		} else {
			err = a.store.RemoveApprovedEmail(r.Context(), email)
			message = "Access removed."
		}
		if err != nil {
			log.Print("[ERROR] auth approval mutation failed")
			reject("The service is temporarily unavailable. Try again in a minute.", http.StatusServiceUnavailable)
			return
		}
		p, _ := r.Context().Value(principalKey{}).(principal)
		//nolint:gosec // G706: every request-derived string is escaped with logutil.SafeString and quoted.
		log.Printf("[AUTH] access change: actor=%q method=%q email=%q", logutil.SafeString(p.UserID), logutil.SafeString(r.Method), logutil.SafeString(email))
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		reject("Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	emails, err := a.store.ApprovedEmails(r.Context())
	if err != nil {
		log.Print("[ERROR] auth approval list failed")
		reject("The service is temporarily unavailable. Try again in a minute.", http.StatusServiceUnavailable)
		return
	}
	visible := nonAdminEmails(emails, a.admins)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = accessPanel.Execute(w, struct {
			Emails  []string
			Message string
		}{visible, message})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"emails": visible})
}

func nonAdminEmails(emails []string, admins map[string]bool) []string {
	visible := make([]string, 0, len(emails))
	for _, email := range emails {
		if !admins[email] {
			visible = append(visible, email)
		}
	}
	return visible
}
