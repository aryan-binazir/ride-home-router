package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
	"github.com/aryan-binazir/ride-home-router/internal/models"
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

func (h *Handler) HandleListActivityLocations(w http.ResponseWriter, r *http.Request) {
	slog.Info("HTTP request", "component", "http", "method", "GET", "path", "/api/v1/activity-locations")
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		slog.Error("Failed to list activity locations", "error", fmt.Sprint(err))
		h.handleInternalError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_list", locations)
		return
	}
	h.writeJSON(w, http.StatusOK, locations)
}

func (h *Handler) HandleListDeletedActivityLocations(w http.ResponseWriter, r *http.Request) {
	slog.Info("HTTP request", "component", "http", "method", "GET", "path", "/api/v1/activity-locations/deleted")
	locations, err := h.DB.ActivityLocations().ListDeleted(r.Context())
	if err != nil {
		slog.Error("Failed to list deleted activity locations", "error", fmt.Sprint(err))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	slog.Info("Listed deleted activity locations", "component", "http", "count", len(locations))
	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_deleted_list", locations)
		return
	}
	h.writeJSON(w, http.StatusOK, locations)
}

func (h *Handler) HandleCreateActivityLocation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	contentType := r.Header.Get(httpx.HeaderContentType)

	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			slog.Info("Form parse failed",
				"component", "http",
				"method", "POST",
				"path", "/api/v1/activity-locations",
				"error", fmt.Sprint(err),
			)
			h.handleValidationError(w, r, messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			slog.Info("Invalid JSON", "component", "http", "method", "POST", "path", "/api/v1/activity-locations", "error", fmt.Sprint(err))
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if message := personLengthMessage(req.Name, req.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" {
		slog.Info("Missing name", "component", "http", "method", "POST", "path", "/api/v1/activity-locations")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageNameRequired)
		return
	}

	if req.Address == "" {
		slog.Info("Missing address", "component", "http", "method", "POST", "path", "/api/v1/activity-locations")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageAddressRequired)
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "POST", "path", "/api/v1/activity-locations")

	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		slog.Error("Failed to geocode activity location address")
		h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(err))
		return
	}

	location := &models.ActivityLocation{
		Name:       req.Name,
		Address:    req.Address,
		Lat:        geocodeResult.Coords.Lat,
		Lng:        geocodeResult.Coords.Lng,
		GeocodedAt: time.Now(),
	}

	createdLocation, err := h.DB.ActivityLocations().Create(r.Context(), location)
	if err != nil {
		slog.Error("Failed to create activity location", "error", fmt.Sprint(err))
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", messageFailedToSaveLocation())
		return
	}

	slog.Info("Created activity location", "component", "http", "id", createdLocation.ID)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityAdded("Location", createdLocation.Name), toastTypeSuccess)
		h.renderTemplate(w, "activity_location_row", createdLocation)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdLocation)
}

func (h *Handler) HandleGetActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "GET",
			"route", "/api/v1/activity-locations/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "GET", "route", "/api/v1/activity-locations/{id}", "id", id)
	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleNotFoundHTMX(w, r, "Location not found. Refresh the page and try again.")
			return
		}
		slog.Error("Failed to get activity location", "id", id, "error", err.Error())
		h.handleInternalError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "activity_location_row", location)
		return
	}

	h.writeJSON(w, http.StatusOK, location)
}

func (h *Handler) HandleActivityLocationForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleNotFoundHTMX(w, r, "Location not found. Refresh the page and try again.")
			return
		}
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "activity_location_form", ActivityLocationFormView{ActivityLocation: location})
}

func (h *Handler) HandleUpdateActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "PUT",
			"route", "/api/v1/activity-locations/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	existing, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		slog.Error("Failed to get activity location for update", "id", id, "error", err.Error())
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
			slog.Info("Form parse failed",
				"component", "http",
				"method", "PUT",
				"route", "/api/v1/activity-locations/{id}",
				"id", id,
				"error", err.Error(),
			)
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			slog.Info("Invalid JSON",
				"component", "http",
				"method", "PUT",
				"route", "/api/v1/activity-locations/{id}",
				"id", id,
				"error", err.Error(),
			)
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
		ID:         id,
		Name:       req.Name,
		Address:    req.Address,
		Lat:        existing.Lat,
		Lng:        existing.Lng,
		GeocodedAt: existing.GeocodedAt,
	}

	if req.Address != existing.Address {
		geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
		if err != nil {
			slog.Error("Failed to geocode updated activity location", "id", id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(err))
			return
		}
		location.Lat = geocodeResult.Coords.Lat
		location.Lng = geocodeResult.Coords.Lng
		location.GeocodedAt = time.Now()
	}

	updatedLocation, err := h.DB.ActivityLocations().Update(r.Context(), location)
	if err != nil {
		slog.Error("Failed to update activity location", "id", id, "error", err.Error())
		if errors.Is(err, database.ErrNotFound) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not save the location. Try again.")
		return
	}

	slog.Info("Updated activity location", "component", "http", "id", updatedLocation.ID)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityUpdated("Location", updatedLocation.Name), toastTypeSuccess)
		h.renderTemplate(w, "activity_location_row", updatedLocation)
		return
	}

	h.writeJSON(w, http.StatusOK, updatedLocation)
}

func (h *Handler) HandleDeleteActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseActivityLocationID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "DELETE",
			"route", "/api/v1/activity-locations/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleValidationError(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "DELETE", "route", "/api/v1/activity-locations/{id}", "id", id)

	if err := h.DB.ActivityLocations().Delete(r.Context(), id); err != nil {
		slog.Error("Failed to delete activity location", "id", id, "error", err.Error())
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

	slog.Info("Deleted activity location", "component", "http", "id", id)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityDeleted("Location"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleRestoreActivityLocation(w http.ResponseWriter, r *http.Request) {
	id, err := parseRestoreID(r)
	if err != nil {
		slog.Info("Invalid ID", "component", "http", "method", "POST", "path", "/api/v1/activity-locations/restore", "error", err.Error())
		h.handleValidationErrorHTMX(w, r, "Choose a valid location. Refresh the page and try again.")
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "POST", "path", "/api/v1/activity-locations/restore", "id", id)
	if err := h.DB.ActivityLocations().Restore(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			slog.Info("Activity location not found for restore", "component", "http", "id", id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Location not found. Refresh the page and try again.")
			return
		}
		slog.Error("Failed to restore activity location", "id", id, "error", err.Error())
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	slog.Info("Restored activity location", "component", "http", "id", id)
	if h.isHTMX(r) {
		h.setHTMXToastWithEvent(w, "rosterRestored", messageEntityRestored("Location"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
