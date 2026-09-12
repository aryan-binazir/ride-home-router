package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
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
		h.handleInternalError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_list", locations)
		return
	}
	h.writeJSON(w, http.StatusOK, locations)
}

// HandleListDeletedActivityLocations handles GET /api/v1/activity-locations/deleted.
func (h *Handler) HandleListDeletedActivityLocations(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/activity-locations/deleted")
	locations, err := h.DB.ActivityLocations().ListDeleted(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to list deleted activity locations: err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	log.Printf("[HTTP] Listed deleted activity locations: count=%d", len(locations))
	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_deleted_list", locations)
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

	contentType := r.Header.Get(httpx.HeaderContentType)

	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/activity-locations: form_parse_error err=%v", err)
			h.handleValidationError(w, r, messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			log.Printf("[HTTP] POST /api/v1/activity-locations: invalid_json err=%v", err)
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if message := personLengthMessage(req.Name, req.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" {
		log.Printf("[HTTP] POST /api/v1/activity-locations: missing name")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageNameRequired)
		return
	}

	if req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/activity-locations: missing address")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageAddressRequired)
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Print("[HTTP] POST /api/v1/activity-locations:")

	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Print("[ERROR] Failed to geocode activity location address")
		h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(err))
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
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", messageFailedToSaveLocation())
		return
	}

	log.Printf("[HTTP] Created activity location: id=%d", createdLocation.ID)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityAdded("Location", createdLocation.Name), toastTypeSuccess)
		h.renderTemplate(w, "activity_location_row", createdLocation)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdLocation)
}

// HandleGetActivityLocation handles GET /api/v1/activity-locations/{id}
func (h *Handler) HandleGetActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] GET /api/v1/activity-locations/{id}: invalid_id path=%s err=%s", logutil.SafeString(r.URL.Path), logutil.SafeString(err.Error()))
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] GET /api/v1/activity-locations/%d", id)
	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleNotFoundHTMX(w, r, "Location not found. Refresh the page and try again.")
			return
		}
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to get activity location: id=%d err=%s", id, logutil.SafeString(err.Error()))
		h.handleInternalError(w, r, err)
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
		h.renderError(w, r, fmt.Errorf("invalid activity location ID"))
		return
	}

	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.renderError(w, r, fmt.Errorf("activity location not found"))
			return
		}
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "activity_location_form", ActivityLocationFormView{ActivityLocation: location})
}

// HandleUpdateActivityLocation handles PUT /api/v1/activity-locations/{id}
func (h *Handler) HandleUpdateActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] PUT /api/v1/activity-locations/{id}: invalid_id path=%s err=%s", logutil.SafeString(r.URL.Path), logutil.SafeString(err.Error()))
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	existing, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to get activity location for update: id=%d err=%s", id, logutil.SafeString(err.Error()))
		h.handleInternalError(w, r, err)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	contentType := r.Header.Get(httpx.HeaderContentType)
	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
			log.Printf("[HTTP] PUT /api/v1/activity-locations/%d: form_parse_error err=%s", id, logutil.SafeString(err.Error()))
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
			log.Printf("[HTTP] PUT /api/v1/activity-locations/%d: invalid_json err=%s", id, logutil.SafeString(err.Error()))
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if message := personLengthMessage(req.Name, req.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageNameRequired)
		return
	}

	if req.Address == "" {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageAddressRequired)
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
			//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
			log.Printf("[ERROR] Failed to geocode updated activity location: id=%d", id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(err))
			return
		}
		location.Lat = geocodeResult.Coords.Lat
		location.Lng = geocodeResult.Coords.Lng
	}

	updatedLocation, err := h.DB.ActivityLocations().Update(r.Context(), location)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to update activity location: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if errors.Is(err, database.ErrNotFound) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not save the location. Try again.")
		return
	}

	log.Printf("[HTTP] Updated activity location: id=%d", updatedLocation.ID)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityUpdated("Location", updatedLocation.Name), toastTypeSuccess)
		h.renderTemplate(w, "activity_location_row", updatedLocation)
		return
	}

	h.writeJSON(w, http.StatusOK, updatedLocation)
}

// HandleDeleteActivityLocation handles DELETE /api/v1/activity-locations/{id}
func (h *Handler) HandleDeleteActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] DELETE /api/v1/activity-locations/{id}: invalid_id path=%s err=%s", logutil.SafeString(r.URL.Path), logutil.SafeString(err.Error()))
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] DELETE /api/v1/activity-locations/%d", id)

	if err := h.DB.ActivityLocations().Delete(r.Context(), id); err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to delete activity location: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if errors.Is(err, database.ErrNotFound) {
			if h.isHTMX(r) {
				h.setHTMXToast(w, "Location not found. Refresh the page and try again.", toastTypeError)
				w.Header().Set(httpx.HeaderHXReswap, httpx.ReswapNone)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h.handleNotFound(w, r, "Location not found. Refresh the page and try again.")
			return
		}
		if h.isHTMX(r) {
			h.setHTMXToast(w, "Failed to delete location", toastTypeError)
			w.Header().Set(httpx.HeaderHXReswap, httpx.ReswapNone)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Deleted activity location: id=%d", id)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityDeleted("Location"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRestoreActivityLocation handles POST /api/v1/activity-locations/restore.
func (h *Handler) HandleRestoreActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseRestoreID(r)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] POST /api/v1/activity-locations/restore: invalid_id err=%s", logutil.SafeString(err.Error()))
		h.handleValidationErrorHTMX(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] POST /api/v1/activity-locations/restore: id=%d", id)
	if err := h.DB.ActivityLocations().Restore(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
			log.Printf("[HTTP] Activity location not found for restore: id=%d", id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to restore activity location: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Restored activity location: id=%d", id)
	if h.isHTMX(r) {
		h.setHTMXToastWithEvent(w, "rosterRestored", messageEntityRestored("Location"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
