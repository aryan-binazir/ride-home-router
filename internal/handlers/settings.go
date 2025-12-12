package handlers

import (
	"encoding/json"
	"net/http"

	"ride-home-router/internal/models"
)

// HandleGetSettings handles GET /api/v1/settings
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.DB.SettingsRepository.Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

// HandleUpdateSettings handles PUT /api/v1/settings
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstituteAddress string `json:"institute_address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.InstituteAddress == "" {
		h.handleValidationError(w, "Institute address is required")
		return
	}

	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.InstituteAddress, 3)
	if err != nil {
		h.handleGeocodingError(w, err)
		return
	}

	settings := &models.Settings{
		InstituteAddress: req.InstituteAddress,
		InstituteLat:     geocodeResult.Coords.Lat,
		InstituteLng:     geocodeResult.Coords.Lng,
	}

	if err := h.DB.SettingsRepository.Update(r.Context(), settings); err != nil {
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}
