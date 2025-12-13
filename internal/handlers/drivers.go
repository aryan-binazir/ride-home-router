package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

// DriverListResponse represents the list response
type DriverListResponse struct {
	Drivers []models.Driver `json:"drivers"`
	Total   int             `json:"total"`
}

// HandleListDrivers handles GET /api/v1/drivers
func (h *Handler) HandleListDrivers(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	log.Printf("[HTTP] GET /api/v1/drivers: search=%s", search)

	drivers, err := h.DB.Drivers().List(r.Context(), search)
	if err != nil {
		log.Printf("[ERROR] Failed to list drivers: search=%s err=%v", search, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Listed drivers: count=%d", len(drivers))
	if h.isHTMX(r) {
		h.renderTemplate(w, "driver_list", map[string]interface{}{
			"Drivers": drivers,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, DriverListResponse{
		Drivers: drivers,
		Total:   len(drivers),
	})
}

// HandleGetDriver handles GET /api/v1/drivers/{id}
func (h *Handler) HandleGetDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/drivers/{id}: id=%d", id)
	driver, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get driver: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	if driver == nil {
		log.Printf("[HTTP] Driver not found: id=%d", id)
		h.handleNotFound(w, "Driver not found")
		return
	}

	h.writeJSON(w, http.StatusOK, driver)
}

// HandleCreateDriver handles POST /api/v1/drivers
func (h *Handler) HandleCreateDriver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string `json:"name"`
		Address            string `json:"address"`
		VehicleCapacity    int    `json:"vehicle_capacity"`
		IsInstituteVehicle bool   `json:"is_institute_vehicle"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
		capacityStr := r.FormValue("vehicle_capacity")
		if capacityStr != "" {
			capacity, err := strconv.Atoi(capacityStr)
			if err != nil {
				h.renderError(w, r, fmt.Errorf("Invalid vehicle capacity"))
				return
			}
			req.VehicleCapacity = capacity
		}
		req.IsInstituteVehicle = r.FormValue("is_institute_vehicle") == "true"
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Name and address are required"))
			return
		}
		h.handleValidationError(w, "Name and address are required")
		return
	}

	if req.VehicleCapacity <= 0 {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Vehicle capacity must be greater than 0"))
			return
		}
		h.handleValidationError(w, "Vehicle capacity must be greater than 0")
		return
	}

	if req.IsInstituteVehicle {
		existing, err := h.DB.Drivers().GetInstituteVehicle(r.Context())
		if err != nil {
			if h.isHTMX(r) {
				h.renderError(w, r, err)
				return
			}
			h.handleInternalError(w, err)
			return
		}
		if existing != nil {
			if h.isHTMX(r) {
				h.renderError(w, r, fmt.Errorf("Institute vehicle already exists"))
				return
			}
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
	}

	log.Printf("[HTTP] POST /api/v1/drivers: name=%s address=%s capacity=%d institute=%v", req.Name, req.Address, req.VehicleCapacity, req.IsInstituteVehicle)
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode driver address: address=%s err=%v", req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleGeocodingError(w, err)
		return
	}

	driver := &models.Driver{
		Name:               req.Name,
		Address:            req.Address,
		Lat:                geocodeResult.Coords.Lat,
		Lng:                geocodeResult.Coords.Lng,
		VehicleCapacity:    req.VehicleCapacity,
		IsInstituteVehicle: req.IsInstituteVehicle,
	}

	driver, err = h.DB.Drivers().Create(r.Context(), driver)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			log.Printf("[HTTP] Driver create conflict: institute vehicle already exists")
			if h.isHTMX(r) {
				h.renderError(w, r, fmt.Errorf("Institute vehicle already exists"))
				return
			}
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
		log.Printf("[ERROR] Failed to create driver: name=%s err=%v", req.Name, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created driver: id=%d name=%s", driver.ID, driver.Name)
	if h.isHTMX(r) {
		drivers, err := h.DB.Drivers().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list drivers after create: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		w.Header().Set("HX-Trigger", "driverCreated")
		h.renderTemplate(w, "driver_list", map[string]interface{}{
			"Drivers": drivers,
		})
		return
	}

	h.writeJSON(w, http.StatusCreated, driver)
}

// HandleUpdateDriver handles PUT /api/v1/drivers/{id}
func (h *Handler) HandleUpdateDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	if strings.HasSuffix(idStr, "/edit") {
		idStr = strings.TrimSuffix(idStr, "/edit")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Invalid driver ID"))
			return
		}
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	log.Printf("[HTTP] PUT /api/v1/drivers/{id}: id=%d", id)
	existing, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err != nil {
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Driver not found"))
			return
		}
		h.handleNotFound(w, "Driver not found")
		return
	}

	var req struct {
		Name               string `json:"name"`
		Address            string `json:"address"`
		VehicleCapacity    int    `json:"vehicle_capacity"`
		IsInstituteVehicle bool   `json:"is_institute_vehicle"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
		capacityStr := r.FormValue("vehicle_capacity")
		if capacityStr != "" {
			capacity, err := strconv.Atoi(capacityStr)
			if err != nil {
				h.renderError(w, r, fmt.Errorf("Invalid vehicle capacity"))
				return
			}
			req.VehicleCapacity = capacity
		}
		req.IsInstituteVehicle = r.FormValue("is_institute_vehicle") == "true"
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Name and address are required"))
			return
		}
		h.handleValidationError(w, "Name and address are required")
		return
	}

	if req.VehicleCapacity <= 0 {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Vehicle capacity must be greater than 0"))
			return
		}
		h.handleValidationError(w, "Vehicle capacity must be greater than 0")
		return
	}

	if req.IsInstituteVehicle && !existing.IsInstituteVehicle {
		instituteVehicle, err := h.DB.Drivers().GetInstituteVehicle(r.Context())
		if err != nil {
			if h.isHTMX(r) {
				h.renderError(w, r, err)
				return
			}
			h.handleInternalError(w, err)
			return
		}
		if instituteVehicle != nil && instituteVehicle.ID != id {
			if h.isHTMX(r) {
				h.renderError(w, r, fmt.Errorf("Institute vehicle already exists"))
				return
			}
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
	}

	driver := &models.Driver{
		ID:                 id,
		Name:               req.Name,
		Address:            req.Address,
		Lat:                existing.Lat,
		Lng:                existing.Lng,
		VehicleCapacity:    req.VehicleCapacity,
		IsInstituteVehicle: req.IsInstituteVehicle,
		CreatedAt:          existing.CreatedAt,
	}

	if req.Address != existing.Address {
		geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
		if err != nil {
			if h.isHTMX(r) {
				h.renderError(w, r, err)
				return
			}
			h.handleGeocodingError(w, err)
			return
		}
		driver.Lat = geocodeResult.Coords.Lat
		driver.Lng = geocodeResult.Coords.Lng
	}

	driver, err = h.DB.Drivers().Update(r.Context(), driver)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			log.Printf("[HTTP] Driver update conflict: id=%d institute vehicle already exists", id)
			if h.isHTMX(r) {
				h.renderError(w, r, fmt.Errorf("Institute vehicle already exists"))
				return
			}
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
		log.Printf("[ERROR] Failed to update driver: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if driver == nil {
		log.Printf("[HTTP] Driver not found after update: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Driver not found"))
			return
		}
		h.handleNotFound(w, "Driver not found")
		return
	}

	log.Printf("[HTTP] Updated driver: id=%d name=%s", driver.ID, driver.Name)
	if h.isHTMX(r) {
		drivers, err := h.DB.Drivers().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list drivers after update: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		w.Header().Set("HX-Trigger", "driverUpdated")
		h.renderTemplate(w, "driver_list", map[string]interface{}{
			"Drivers": drivers,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, driver)
}

// HandleDeleteDriver handles DELETE /api/v1/drivers/{id}
func (h *Handler) HandleDeleteDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Invalid driver ID"))
			return
		}
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/drivers/{id}: id=%d", id)
	err = h.DB.Drivers().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Driver not found for delete: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Driver not found"))
			return
		}
		h.handleNotFound(w, "Driver not found")
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete driver: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted driver: id=%d", id)
	if h.isHTMX(r) {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleDriverForm handles GET /api/v1/drivers/new and GET /api/v1/drivers/{id}/edit
func (h *Handler) HandleDriverForm(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	idStr = strings.TrimSuffix(idStr, "/edit")

	var driver *models.Driver
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.renderError(w, r, fmt.Errorf("Invalid driver ID"))
			return
		}

		driver, err = h.DB.Drivers().GetByID(r.Context(), id)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		if driver == nil {
			h.renderError(w, r, fmt.Errorf("Driver not found"))
			return
		}
	} else {
		driver = &models.Driver{}
	}

	h.renderTemplate(w, "driver_form", map[string]interface{}{
		"Driver": driver,
	})
}
