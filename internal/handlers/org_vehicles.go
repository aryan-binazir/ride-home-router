package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

func parseOrgVehicleID(path string) (int64, error) {
	idStr := strings.TrimPrefix(path, "/api/v1/org-vehicles/")
	idStr = strings.TrimSuffix(idStr, "/edit")
	return strconv.ParseInt(idStr, 10, 64)
}

// HandleListOrgVehicles handles GET /api/v1/org-vehicles
func (h *Handler) HandleListOrgVehicles(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/org-vehicles")
	vehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to list organization vehicles: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, vehicles)
}

// HandleCreateOrgVehicle handles POST /api/v1/org-vehicles
func (h *Handler) HandleCreateOrgVehicle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	}

	contentType := r.Header.Get("Content-Type")

	// Handle form data (from htmx)
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/org-vehicles: form_parse_error err=%v", err)
			h.handleValidationErrorHTMX(w, r, "Invalid form data")
			return
		}
		req.Name = r.FormValue("name")
		if capStr := r.FormValue("capacity"); capStr != "" {
			cap, err := strconv.Atoi(capStr)
			if err != nil {
				h.handleValidationErrorHTMX(w, r, "Invalid capacity")
				return
			}
			req.Capacity = cap
		}
	} else {
		// Handle JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/org-vehicles: invalid_json err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" {
		log.Printf("[HTTP] POST /api/v1/org-vehicles: missing name")
		h.handleValidationErrorHTMX(w, r, "Name is required")
		return
	}

	if req.Capacity < 1 {
		log.Printf("[HTTP] POST /api/v1/org-vehicles: invalid capacity=%d", req.Capacity)
		h.handleValidationErrorHTMX(w, r, "Capacity must be at least 1")
		return
	}

	log.Printf("[HTTP] POST /api/v1/org-vehicles: name=%s capacity=%d", req.Name, req.Capacity)

	vehicle := &models.OrganizationVehicle{
		Name:     req.Name,
		Capacity: req.Capacity,
	}

	createdVehicle, err := h.DB.OrganizationVehicles().Create(r.Context(), vehicle)
	if err != nil {
		log.Printf("[ERROR] Failed to create organization vehicle: err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created organization vehicle: id=%d name=%s capacity=%d",
		createdVehicle.ID, createdVehicle.Name, createdVehicle.Capacity)

	if h.isHTMX(r) {
		// Return the new vehicle row HTML and trigger a success toast
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Van '%s' added!", "type": "success"}}`, html.EscapeString(createdVehicle.Name)))
		h.renderTemplate(w, "org_vehicle_row", createdVehicle)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdVehicle)
}

// HandleGetOrgVehicle handles GET /api/v1/org-vehicles/{id}
func (h *Handler) HandleGetOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/org-vehicles/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationError(w, "Invalid organization vehicle ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/org-vehicles/%d", id)
	vehicle, err := h.DB.OrganizationVehicles().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get organization vehicle: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}
	if vehicle == nil {
		h.handleNotFoundHTMX(w, r, "Organization vehicle not found")
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "org_vehicle_row", vehicle)
		return
	}

	h.writeJSON(w, http.StatusOK, vehicle)
}

// HandleOrgVehicleForm handles GET /api/v1/org-vehicles/{id}/edit
func (h *Handler) HandleOrgVehicleForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		h.renderError(w, r, fmt.Errorf("Invalid organization vehicle ID"))
		return
	}

	vehicle, err := h.DB.OrganizationVehicles().GetByID(r.Context(), id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	if vehicle == nil {
		h.renderError(w, r, fmt.Errorf("Organization vehicle not found"))
		return
	}

	h.renderTemplate(w, "org_vehicle_form", map[string]interface{}{
		"OrgVehicle": vehicle,
	})
}

// HandleUpdateOrgVehicle handles PUT /api/v1/org-vehicles/{id}
func (h *Handler) HandleUpdateOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/org-vehicles/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationErrorHTMX(w, r, "Invalid organization vehicle ID")
		return
	}

	var req struct {
		Name     string `json:"name"`
		Capacity int    `json:"capacity"`
	}

	contentType := r.Header.Get("Content-Type")

	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			if h.isHTMX(r) {
				h.setHTMXToast(w, "Invalid form data", "error")
				w.Header().Set("HX-Reswap", "none")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			h.handleValidationError(w, "Invalid form data")
			return
		}
		req.Name = r.FormValue("name")
		if capStr := r.FormValue("capacity"); capStr != "" {
			cap, err := strconv.Atoi(capStr)
			if err != nil {
				if h.isHTMX(r) {
					h.setHTMXToast(w, "Invalid capacity", "error")
					w.Header().Set("HX-Reswap", "none")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				h.handleValidationError(w, "Invalid capacity")
				return
			}
			req.Capacity = cap
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" {
		if h.isHTMX(r) {
			h.setHTMXToast(w, "Name is required", "error")
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Name is required")
		return
	}

	if req.Capacity < 1 {
		if h.isHTMX(r) {
			h.setHTMXToast(w, "Capacity must be at least 1", "error")
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Capacity must be at least 1")
		return
	}

	log.Printf("[HTTP] PUT /api/v1/org-vehicles/%d: name=%s capacity=%d", id, req.Name, req.Capacity)

	vehicle := &models.OrganizationVehicle{
		ID:       id,
		Name:     req.Name,
		Capacity: req.Capacity,
	}

	updatedVehicle, err := h.DB.OrganizationVehicles().Update(r.Context(), vehicle)
	if err != nil {
		log.Printf("[ERROR] Failed to update organization vehicle: id=%d err=%v", id, err)
		if errors.Is(err, database.ErrNotFound) {
			if h.isHTMX(r) {
				h.setHTMXToast(w, "Organization vehicle not found", "error")
				w.Header().Set("HX-Reswap", "none")
				w.WriteHeader(http.StatusNotFound)
				return
			}
			h.handleNotFound(w, "Organization vehicle not found")
			return
		}
		if h.isHTMX(r) {
			h.setHTMXToast(w, "Failed to update van", "error")
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated organization vehicle: id=%d", id)

	if h.isHTMX(r) {
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Van '%s' updated!", "type": "success"}}`, html.EscapeString(updatedVehicle.Name)))
		h.renderTemplate(w, "org_vehicle_row", updatedVehicle)
		return
	}

	h.writeJSON(w, http.StatusOK, updatedVehicle)
}

// HandleDeleteOrgVehicle handles DELETE /api/v1/org-vehicles/{id}
func (h *Handler) HandleDeleteOrgVehicle(w http.ResponseWriter, r *http.Request) {
	id, err := parseOrgVehicleID(r.URL.Path)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/org-vehicles/{id}: invalid_id path=%s err=%v", r.URL.Path, err)
		h.handleValidationErrorHTMX(w, r, "Invalid organization vehicle ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/org-vehicles/%d", id)

	if err := h.DB.OrganizationVehicles().Delete(r.Context(), id); err != nil {
		log.Printf("[ERROR] Failed to delete organization vehicle: id=%d err=%v", id, err)
		if errors.Is(err, database.ErrNotFound) {
			h.handleNotFoundHTMX(w, r, "Organization vehicle not found")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted organization vehicle: id=%d", id)

	if h.isHTMX(r) {
		// Return 200 with empty body so htmx will swap (remove) the element
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Van deleted", "type": "success"}}`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
