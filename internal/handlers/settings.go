package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

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
		InstituteAddress string `json:"institute_address"`
		UseMiles         bool   `json:"use_miles"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		req.InstituteAddress = r.FormValue("institute_address")
		req.UseMiles = r.FormValue("use_miles") == "on" || r.FormValue("use_miles") == "true"
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/settings: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.InstituteAddress == "" {
		log.Printf("[HTTP] PUT /api/v1/settings: missing institute_address")
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Institute address is required"))
			return
		}
		h.handleValidationError(w, "Institute address is required")
		return
	}

	log.Printf("[HTTP] PUT /api/v1/settings: institute_address=%s", req.InstituteAddress)
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.InstituteAddress, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode institute address: address=%s err=%v", req.InstituteAddress, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleGeocodingError(w, err)
		return
	}

	settings := &models.Settings{
		InstituteAddress: req.InstituteAddress,
		InstituteLat:     geocodeResult.Coords.Lat,
		InstituteLng:     geocodeResult.Coords.Lng,
		UseMiles:         req.UseMiles,
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

	log.Printf("[HTTP] Updated settings: lat=%.6f lng=%.6f", settings.InstituteLat, settings.InstituteLng)
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-success">Settings saved successfully! Coordinates: %.6f, %.6f</div>`,
			settings.InstituteLat, settings.InstituteLng)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}
