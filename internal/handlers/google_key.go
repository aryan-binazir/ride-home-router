package handlers

import (
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"ride-home-router/internal/access"
	"strings"
)

var googleKeyPanel = template.Must(template.New("google-key").Parse(`<section id="google-maps-key" class="card">
<h3>Google Maps API key</h3>
<p>{{if .Configured}}<span aria-label="Key configured">••••••••</span> Configured{{else}}Not configured{{end}}</p>
<form hx-put="/api/v1/settings/google-maps-key" hx-target="#google-maps-key" hx-swap="outerHTML">
<div class="form-group"><label class="form-label" for="google-key-input">{{if .Configured}}Replace key{{else}}API key{{end}}</label>
<input id="google-key-input" class="form-input" type="password" name="api_key" autocomplete="new-password" spellcheck="false" autocapitalize="off" required maxlength="4096"></div>
<button class="btn btn-primary" type="submit">{{if .Configured}}Replace key{{else}}Save key{{end}}</button>
</form>
{{if .Configured}}<button class="btn" type="button" hx-delete="/api/v1/settings/google-maps-key" hx-target="#google-maps-key" hx-swap="outerHTML" hx-confirm="Delete the Google Maps API key? Route calculation will be unavailable until a new key is saved.">Delete key</button>{{end}}
</section>`))

// HandleGoogleMapsKey exposes only status and write operations, never the saved key.
func (h *Handler) HandleGoogleMapsKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !access.IsAdmin(r.Context()) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	reject := func(message string, status int) {
		if h.isHTMX(r) {
			h.setHTMXToast(w, message, toastTypeError)
			w.Header().Set("HX-Reswap", "none")
		}
		http.Error(w, message, status)
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		// Secrets are accepted only in the body. Query strings can enter access logs.
		if r.URL.RawQuery != "" {
			reject("Invalid request", http.StatusBadRequest)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		var input struct {
			APIKey string `json:"api_key"`
		}
		if h.isHTMX(r) {
			if err := r.ParseForm(); err != nil {
				reject("Invalid request", http.StatusBadRequest)
				return
			}
			input.APIKey = r.PostForm.Get("api_key")
		} else {
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				reject("Invalid request", http.StatusBadRequest)
				return
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				reject("Invalid request", http.StatusBadRequest)
				return
			}
		}
		key := strings.TrimSpace(input.APIKey)
		if key == "" || key == "********" || len(key) > 4096 || strings.IndexFunc(key, func(c rune) bool {
			return (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-'
		}) >= 0 {
			reject("Enter a valid API key", http.StatusBadRequest)
			return
		}
		if err := h.DB.Settings().SetGoogleMapsKey(r.Context(), key); err != nil {
			log.Print("[ERROR] Google Maps credential update failed")
			reject("Unable to save key", http.StatusServiceUnavailable)
			return
		}
		log.Print("[ADMIN] Google Maps credential replaced")
	case http.MethodDelete:
		if err := h.DB.Settings().DeleteGoogleMapsKey(r.Context()); err != nil {
			log.Print("[ERROR] Google Maps credential deletion failed")
			reject("Unable to delete key", http.StatusServiceUnavailable)
			return
		}
		log.Print("[ADMIN] Google Maps credential deleted")
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		reject("Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	configured, err := h.DB.Settings().GoogleMapsKeyConfigured(r.Context())
	if err != nil {
		log.Print("[ERROR] Google Maps credential status lookup failed")
		reject("Unable to load key status", http.StatusServiceUnavailable)
		return
	}
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = googleKeyPanel.Execute(w, struct{ Configured bool }{configured})
		return
	}
	h.writeJSON(w, http.StatusOK, struct {
		Configured bool `json:"configured"`
	}{configured})
}
