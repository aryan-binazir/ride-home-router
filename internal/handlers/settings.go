package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"ride-home-router/internal/models"
)

// HandleGetSettings handles GET /api/v1/settings
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/settings")
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to get settings: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

// HandleUpdateSettings handles PUT /api/v1/settings
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SelectedActivityLocationID int64 `json:"selected_activity_location_id"`
		UseMiles                   bool  `json:"use_miles"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		if idStr := r.FormValue("selected_activity_location_id"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				req.SelectedActivityLocationID = id
			}
		}
		req.UseMiles = r.FormValue("use_miles") == "on" || r.FormValue("use_miles") == "true"
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/settings: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.SelectedActivityLocationID == 0 {
		log.Printf("[HTTP] PUT /api/v1/settings: missing selected_activity_location_id")
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Please select an activity location"))
			return
		}
		h.handleValidationError(w, "Activity location is required")
		return
	}

	// Verify the location exists
	location, err := h.DB.ActivityLocations().GetByID(r.Context(), req.SelectedActivityLocationID)
	if err != nil {
		log.Printf("[ERROR] Failed to get activity location: err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if location == nil {
		log.Printf("[HTTP] PUT /api/v1/settings: activity location not found: id=%d", req.SelectedActivityLocationID)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Selected activity location not found"))
			return
		}
		h.handleNotFound(w, "Activity location not found")
		return
	}

	settings := &models.Settings{
		SelectedActivityLocationID: req.SelectedActivityLocationID,
		UseMiles:                   req.UseMiles,
	}

	if err := h.DB.Settings().Update(r.Context(), settings); err != nil {
		log.Printf("[ERROR] Failed to update settings: err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated settings: selected_location_id=%d", settings.SelectedActivityLocationID)
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-success">Settings saved successfully! Using: %s</div>`, location.Name)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}
