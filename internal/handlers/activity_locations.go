package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

func parseActivityLocationID(path string) (int64, error) {
	idStr := strings.TrimPrefix(path, "/api/v1/activity-locations/")
	idStr = strings.TrimSuffix(idStr, "/edit")
	idStr = strings.Trim(idStr, "/")
	if idStr == "" || strings.Contains(idStr, "/") {
		return 0, fmt.Errorf("invalid activity location path")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

// HandleListActivityLocations handles GET /api/v1/activity-locations
func (h *Handler) HandleListActivityLocations(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/activity-locations")
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to list activity locations: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, locations)
}

// HandleCreateActivityLocation handles POST /api/v1/activity-locations
func (h *Handler) HandleCreateActivityLocation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	contentType := r.Header.Get("Content-Type")

	// Handle form data (from htmx)
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/activity-locations: form_parse_error err=%v", err)
			h.handleValidationError(w, "Invalid form data")
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		// Handle JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/activity-locations: invalid_json err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" {
		log.Printf("[HTTP] POST /api/v1/activity-locations: missing name")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Name is required")
		return
	}

	if req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/activity-locations: missing address")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Address is required")
		return
	}

	log.Printf("[HTTP] POST /api/v1/activity-locations: name=%s address=%s", req.Name, req.Address)

	// Geocode the address
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode address: address=%s err=%v", req.Address, err)
		h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", fmt.Sprintf("Failed to geocode address: %s", err.Error()))
		return
	}

	location := &models.ActivityLocation{
		Name:    req.Name,
		Address: req.Address,
		Lat:     geocodeResult.Coords.Lat,
		Lng:     geocodeResult.Coords.Lng,
	}

	createdLocation, err := h.DB.ActivityLocations().Create(r.Context(), location)
	if err != nil {
		log.Printf("[ERROR] Failed to create activity location: err=%v", err)
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fmt.Sprintf("Failed to save location: %s", err.Error()))
		return
	}

	log.Printf("[HTTP] Created activity location: id=%d name=%s lat=%.6f lng=%.6f",
		createdLocation.ID, createdLocation.Name, createdLocation.Lat, createdLocation.Lng)

	if h.isHTMX(r) {
		// Return the new location row HTML and trigger a success toast
		h.setHTMXToast(w, fmt.Sprintf("Location '%s' added!", createdLocation.Name), "success")
		h.renderTemplate(w, "activity_location_row", createdLocation)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdLocation)
}

// HandleGetActivityLocation handles GET /api/v1/activity-locations/{id}
func (h *Handler) HandleGetActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/activity-locations/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationError(w, "Invalid activity location ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/activity-locations/%d", id)
	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get activity location: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}
	if location == nil {
		h.handleNotFoundHTMX(w, r, "Activity location not found")
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_row", location)
		return
	}

	h.writeJSON(w, http.StatusOK, location)
}

// HandleActivityLocationForm handles GET /api/v1/activity-locations/{id}/edit
func (h *Handler) HandleActivityLocationForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		h.renderError(w, r, fmt.Errorf("Invalid activity location ID"))
		return
	}

	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if location == nil {
		h.renderError(w, r, fmt.Errorf("Activity location not found"))
		return
	}

	h.renderTemplate(w, "activity_location_form", map[string]interface{}{
		"ActivityLocation": location,
	})
}

// HandleUpdateActivityLocation handles PUT /api/v1/activity-locations/{id}
func (h *Handler) HandleUpdateActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/activity-locations/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationError(w, "Invalid activity location ID")
		return
	}

	existing, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get activity location for update: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Activity location not found")
		return
	}

	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] PUT /api/v1/activity-locations/%d: form_parse_error err=%v", id, err)
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid form data")
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/activity-locations/%d: invalid_json err=%v", id, err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Name is required")
		return
	}

	if req.Address == "" {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Address is required")
		return
	}

	location := &models.ActivityLocation{
		ID:      id,
		Name:    req.Name,
		Address: req.Address,
		Lat:     existing.Lat,
		Lng:     existing.Lng,
	}

	if req.Address != existing.Address {
		geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
		if err != nil {
			log.Printf("[ERROR] Failed to geocode updated activity location: id=%d address=%s err=%v", id, req.Address, err)
			h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", fmt.Sprintf("Failed to geocode address: %s", err.Error()))
			return
		}
		location.Lat = geocodeResult.Coords.Lat
		location.Lng = geocodeResult.Coords.Lng
	}

	updatedLocation, err := h.DB.ActivityLocations().Update(r.Context(), location)
	if err != nil {
		log.Printf("[ERROR] Failed to update activity location: id=%d err=%v", id, err)
		if errors.Is(err, database.ErrNotFound) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Activity location not found")
			return
		}
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to update location")
		return
	}

	log.Printf("[HTTP] Updated activity location: id=%d name=%s", updatedLocation.ID, updatedLocation.Name)

	if h.isHTMX(r) {
		h.setHTMXToast(w, fmt.Sprintf("Location '%s' updated!", updatedLocation.Name), "success")
		h.renderTemplate(w, "activity_location_row", updatedLocation)
		return
	}

	h.writeJSON(w, http.StatusOK, updatedLocation)
}

// HandleDeleteActivityLocation handles DELETE /api/v1/activity-locations/{id}
func (h *Handler) HandleDeleteActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/activity-locations/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationError(w, "Invalid activity location ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/activity-locations/%d", id)

	if err := h.DB.ActivityLocations().Delete(r.Context(), id); err != nil {
		log.Printf("[ERROR] Failed to delete activity location: id=%d err=%v", id, err)
		if errors.Is(err, database.ErrNotFound) {
			if h.isHTMX(r) {
				h.setHTMXToast(w, "Activity location not found", "error")
				w.Header().Set("HX-Reswap", "none")
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h.handleNotFound(w, "Activity location not found")
			return
		}
		if h.isHTMX(r) {
			h.setHTMXToast(w, "Failed to delete location", "error")
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted activity location: id=%d", id)

	if h.isHTMX(r) {
		// Return 200 with empty body so htmx will swap (remove) the element
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Location deleted", "type": "success"}}`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
