package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/routing"
)

// TemplateSet holds base templates and page templates separately
type TemplateSet struct {
	Base  *template.Template
	Pages map[string]string
	Funcs template.FuncMap
}

// Handler provides common handler utilities and dependencies
type Handler struct {
	DB           database.DataStore
	Geocoder     geocoding.Geocoder
	DistanceCalc distance.DistanceCalculator
	Router       routing.Router
	Templates    *TemplateSet
	RouteSession *RouteSessionStore
}

// ErrorResponse represents an API error
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information
type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// isHTMX checks if the request is an htmx request
func (h *Handler) isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// writeJSON writes a JSON response
func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a JSON error response
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	h.writeJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// handleNotFound handles 404 errors
func (h *Handler) handleNotFound(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusNotFound, "NOT_FOUND", message, nil)
}

// handleNotFoundHTMX handles 404 errors with htmx support
func (h *Handler) handleNotFoundHTMX(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `<div class="alert alert-warning">%s</div>`, html.EscapeString(message))
		return
	}
	h.handleNotFound(w, message)
}

// handleValidationError handles 400 errors
func (h *Handler) handleValidationError(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

// handleValidationErrorHTMX handles 400 errors with htmx support
func (h *Handler) handleValidationErrorHTMX(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<div class="alert alert-warning">%s</div>`, html.EscapeString(message))
		return
	}
	h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

// handleGeocodingError handles 422 errors for geocoding failures
func (h *Handler) handleGeocodingError(w http.ResponseWriter, err error) {
	h.writeError(w, http.StatusUnprocessableEntity, "GEOCODING_FAILED", err.Error(), nil)
}

// handleRoutingError handles 422 errors for routing failures
func (h *Handler) handleRoutingError(w http.ResponseWriter, err error) {
	if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
		h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", rerr.Reason, map[string]interface{}{
			"unassigned_count":   rerr.UnassignedCount,
			"total_capacity":     rerr.TotalCapacity,
			"total_participants": rerr.TotalParticipants,
		})
		return
	}
	h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", err.Error(), nil)
}

// handleInternalError handles 500 errors
func (h *Handler) handleInternalError(w http.ResponseWriter, err error) {
	log.Printf("[ERROR] Internal error: %v", err)
	h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "An error occurred. Please try again.", nil)
}

// checkNotFound checks if an error is a not found error
func (h *Handler) checkNotFound(err error) bool {
	return errors.Is(err, database.ErrNotFound)
}

// renderTemplate renders an HTML template
func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Always clone to avoid "cannot Clone after executed" error
	tmpl, err := h.Templates.Base.Clone()
	if err != nil {
		log.Printf("[ERROR] Template clone error: template=%s err=%v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Check if this is a page template (has content in Pages map)
	if pageContent, ok := h.Templates.Pages[name]; ok {
		// Parse the page template (which defines "content")
		_, err = tmpl.New(name).Parse(pageContent)
		if err != nil {
			log.Printf("[ERROR] Template parse error: template=%s err=%v", name, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Execute layout.html (which includes {{template "content" .}})
		if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
			log.Printf("[ERROR] Template execute error: template=%s err=%v", name, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// For partials, execute from the cloned template
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("[ERROR] Template partial error: template=%s err=%v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// renderError renders an error response (JSON for API, HTML for htmx)
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, err error) {
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<div class="alert alert-error">%s</div>`, html.EscapeString(err.Error()))
		return
	}
	h.handleInternalError(w, err)
}
