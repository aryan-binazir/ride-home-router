package access

import (
	"encoding/json"
	"html/template"
	"net/http"
)

// Register adds authentication and admin-only access management to the router.
// The entire router must still be wrapped with Protect.
func (a *Access) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /sign-in", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in - Ride Home Router</title><link rel="stylesheet" href="/static/css/style.css"><script src="/static/js/auth.js" defer></script></head>
<body>
<main class="main"><h1>Ride Home Router</h1><p id="auth-message">Sign in with your approved account.</p><div id="clerk-sign-in"></div><button type="button" data-sign-out hidden>Sign out</button></main>
</body>
</html>`))
	})
	mux.HandleFunc("GET /auth/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"publishableKey": a.cfg.PublishableKey, "scriptURL": a.issuer + "/npm/@clerk/clerk-js@5/dist/clerk.browser.js"})
	})
	mux.HandleFunc("/api/v1/access", a.manage)
}

var accessPanel = template.Must(template.New("access").Parse(`<section id="access-management" class="card">
<h2>Approved emails</h2>
<p>Approved accounts share all application data. Administrators are configured in Railway.</p>
{{if .Message}}<p role="status">{{.Message}}</p>{{end}}
<form hx-post="/api/v1/access" hx-target="#access-management" hx-swap="outerHTML"><label for="approved-email">Email address</label><input id="approved-email" class="form-input" type="email" name="email" required maxlength="254"><button class="btn btn-primary" type="submit">Approve email</button></form>
<ul>{{range .Emails}}<li>{{.}} <form hx-delete="/api/v1/access" hx-target="#access-management" hx-swap="outerHTML"><input type="hidden" name="email" value="{{.}}"><button class="btn" type="submit">Remove access</button></form></li>{{else}}<li>No approved non-admin emails.</li>{{end}}</ul>
</section>`))

func (a *Access) manage(w http.ResponseWriter, r *http.Request) {
	reject := func(message string, status int) {
		if r.Header.Get("HX-Request") == "true" {
			payload, _ := json.Marshal(map[string]any{"showToast": map[string]string{"message": message, "type": "error"}})
			w.Header().Set("HX-Trigger", string(payload))
			w.Header().Set("HX-Reswap", "none")
		}
		http.Error(w, message, status)
	}
	if !IsAdmin(r.Context()) {
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
			reject("Administrators are managed through ADMIN_EMAILS", http.StatusBadRequest)
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
			reject("Service Unavailable", http.StatusServiceUnavailable)
			return
		}
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		reject("Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	emails, err := a.store.ApprovedEmails(r.Context())
	if err != nil {
		reject("Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	// An email promoted through the environment is not editable in this UI.
	visible := make([]string, 0, len(emails))
	for _, email := range emails {
		if !a.admins[email] {
			visible = append(visible, email)
		}
	}
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
