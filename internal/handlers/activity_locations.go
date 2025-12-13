package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

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
		if h.isHTMX(r) {
			http.Error(w, "Name is required", http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Name is required")
		return
	}

	if req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/activity-locations: missing address")
		if h.isHTMX(r) {
			http.Error(w, "Address is required", http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Address is required")
		return
	}

	log.Printf("[HTTP] POST /api/v1/activity-locations: name=%s address=%s", req.Name, req.Address)

	// Geocode the address
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode address: address=%s err=%v", req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleGeocodingError(w, err)
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
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created activity location: id=%d name=%s lat=%.6f lng=%.6f",
		createdLocation.ID, createdLocation.Name, createdLocation.Lat, createdLocation.Lng)

	if h.isHTMX(r) {
		// Return success message and refresh the location list
		w.Header().Set("HX-Trigger", "activity-location-created")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.Error(w, "", http.StatusNoContent)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdLocation)
}

// HandleDeleteActivityLocation handles DELETE /api/v1/activity-locations/{id}
func (h *Handler) HandleDeleteActivityLocation(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		h.handleNotFound(w, "Activity location not found")
		return
	}

	id, err := strconv.ParseInt(pathParts[3], 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/activity-locations/{id}: invalid_id id=%s", pathParts[3])
		h.handleValidationError(w, "Invalid activity location ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/activity-locations/%d", id)

	if err := h.DB.ActivityLocations().Delete(r.Context(), id); err != nil {
		log.Printf("[ERROR] Failed to delete activity location: id=%d err=%v", id, err)
		if strings.Contains(err.Error(), "not found") {
			h.handleNotFound(w, "Activity location not found")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted activity location: id=%d", id)

	if h.isHTMX(r) {
		// Return 200 with empty body so htmx will swap (remove) the element
		w.Header().Set("HX-Trigger", "activity-location-deleted")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
