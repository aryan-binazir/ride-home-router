package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

func parseOrgVehicleID(path string) (int64, error) {
	idStr := strings.TrimPrefix(path, "/api/v1/org-vehicles/")
	idStr = strings.TrimSuffix(idStr, "/edit")
	idStr = strings.Trim(idStr, "/")
	if idStr == "" || strings.Contains(idStr, "/") {
		return 0, fmt.Errorf("invalid organization vehicle path")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *Handler) HandleListOrgVehicles(w http.ResponseWriter, r *http.Request) {
	slog.Info("HTTP request", "component", "http", "method", "GET", "path", "/api/v1/org-vehicles")
	vehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		slog.Error("Failed to list organization vehicles", "error", fmt.Sprint(err))
		h.handleInternalError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, vehicles)
}

func (h *Handler) HandleCreateOrgVehicle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	}

	contentType := r.Header.Get(httpx.HeaderContentType)

	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			slog.Info("Form parse failed", "component", "http", "method", "POST", "path", "/api/v1/org-vehicles", "error", fmt.Sprint(err))
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		if capStr := r.FormValue("capacity"); capStr != "" {
			cap, err := strconv.Atoi(capStr)
			if err != nil {
				h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidCapacity)
				return
			}
			req.Capacity = cap
		}
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			slog.Info("Invalid JSON", "component", "http", "method", "POST", "path", "/api/v1/org-vehicles", "error", fmt.Sprint(err))
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if message := fieldLengthMessage("Name", req.Name, models.MaxNameLength); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" {
		slog.Info("Missing name", "component", "http", "method", "POST", "path", "/api/v1/org-vehicles")
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageNameRequired)
		return
	}

	if req.Capacity < 1 {
		slog.Info("Invalid capacity", "component", "http", "method", "POST", "path", "/api/v1/org-vehicles", "capacity", req.Capacity)
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageOrganizationVehicleCapacityMustBeAtLeastOne)
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "POST", "path", "/api/v1/org-vehicles", "capacity", req.Capacity)

	vehicle := &models.OrganizationVehicle{
		Name:     req.Name,
		Capacity: req.Capacity,
	}

	createdVehicle, err := h.DB.OrganizationVehicles().Create(r.Context(), vehicle)
	if err != nil {
		slog.Error("Failed to create organization vehicle", "error", fmt.Sprint(err))
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", messageFailedToSaveVan())
		return
	}

	slog.Info("Created organization vehicle", "component", "http", "id", createdVehicle.ID, "capacity", createdVehicle.Capacity)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityAdded("Van", createdVehicle.Name), toastTypeSuccess)
		h.renderTemplate(w, "org_vehicle_row", createdVehicle)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdVehicle)
}

func (h *Handler) HandleGetOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "GET",
			"route", "/api/v1/org-vehicles/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleValidationError(w, r, messageInvalidOrganizationVehicleID)
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "GET", "route", "/api/v1/org-vehicles/{id}", "id", id)
	vehicle, err := h.DB.OrganizationVehicles().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleNotFoundHTMX(w, r, messageOrganizationVehicleNotFound)
			return
		}
		slog.Error("Failed to get organization vehicle", "id", id, "error", err.Error())
		h.handleInternalError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "org_vehicle_row", vehicle)
		return
	}

	h.writeJSON(w, http.StatusOK, vehicle)
}

func (h *Handler) HandleOrgVehicleForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidOrganizationVehicleID)
		return
	}

	vehicle, err := h.DB.OrganizationVehicles().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			h.handleNotFoundHTMX(w, r, messageOrganizationVehicleNotFound)
			return
		}
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "org_vehicle_form", OrgVehicleFormView{OrgVehicle: vehicle})
}

func (h *Handler) HandleUpdateOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "PUT",
			"route", "/api/v1/org-vehicles/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidOrganizationVehicleID)
		return
	}

	var req struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	}

	contentType := r.Header.Get(httpx.HeaderContentType)

	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = r.FormValue("name")
		if capStr := r.FormValue("capacity"); capStr != "" {
			cap, err := strconv.Atoi(capStr)
			if err != nil {
				h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidCapacity)
				return
			}
			req.Capacity = cap
		}
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if message := fieldLengthMessage("Name", req.Name, models.MaxNameLength); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageNameRequired)
		return
	}

	if req.Capacity < 1 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageOrganizationVehicleCapacityMustBeAtLeastOne)
		return
	}

	slog.Info("HTTP request",
		"component", "http",
		"method", "PUT",
		"route", "/api/v1/org-vehicles/{id}",
		"id", id,
		"capacity", req.Capacity,
	)

	vehicle := &models.OrganizationVehicle{
		ID:       id,
		Name:     req.Name,
		Capacity: req.Capacity,
	}

	updatedVehicle, err := h.DB.OrganizationVehicles().Update(r.Context(), vehicle)
	if err != nil {
		slog.Error("Failed to update organization vehicle", "id", id, "error", err.Error())
		if errors.Is(err, database.ErrNotFound) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageOrganizationVehicleNotFound)
			return
		}
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not save the van. Try again.")
		return
	}

	slog.Info("Updated organization vehicle", "component", "http", "id", id)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityUpdated("Van", updatedVehicle.Name), toastTypeSuccess)
		h.renderTemplate(w, "org_vehicle_row", updatedVehicle)
		return
	}

	h.writeJSON(w, http.StatusOK, updatedVehicle)
}

func (h *Handler) HandleDeleteOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		slog.Info("Invalid ID",
			"component", "http",
			"method", "DELETE",
			"route", "/api/v1/org-vehicles/{id}",
			"path", r.URL.Path,
			"error", err.Error(),
		)
		h.handleValidationErrorHTMX(w, r, messageInvalidOrganizationVehicleID)
		return
	}

	slog.Info("HTTP request", "component", "http", "method", "DELETE", "route", "/api/v1/org-vehicles/{id}", "id", id)

	if err := h.DB.OrganizationVehicles().Delete(r.Context(), id); err != nil {
		slog.Error("Failed to delete organization vehicle", "id", id, "error", err.Error())
		if errors.Is(err, database.ErrNotFound) {
			h.handleNotFoundHTMX(w, r, messageOrganizationVehicleNotFound)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	slog.Info("Deleted organization vehicle", "component", "http", "id", id)

	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityDeleted("Van"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
