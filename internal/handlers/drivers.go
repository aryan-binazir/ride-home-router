package handlers

import (
	"encoding/json"
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

	drivers, err := h.DB.DriverRepository.List(r.Context(), search)
	if err != nil {
		h.handleInternalError(w, err)
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
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	driver, err := h.DB.DriverRepository.GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if driver == nil {
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

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Name == "" || req.Address == "" {
		h.handleValidationError(w, "Name and address are required")
		return
	}

	if req.VehicleCapacity <= 0 {
		h.handleValidationError(w, "Vehicle capacity must be greater than 0")
		return
	}

	if req.IsInstituteVehicle {
		existing, err := h.DB.DriverRepository.GetInstituteVehicle(r.Context())
		if err != nil {
			h.handleInternalError(w, err)
			return
		}
		if existing != nil {
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
	}

	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
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

	driver, err = h.DB.DriverRepository.Create(r.Context(), driver)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, driver)
}

// HandleUpdateDriver handles PUT /api/v1/drivers/{id}
func (h *Handler) HandleUpdateDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	existing, err := h.DB.DriverRepository.GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		h.handleNotFound(w, "Driver not found")
		return
	}

	var req struct {
		Name               string `json:"name"`
		Address            string `json:"address"`
		VehicleCapacity    int    `json:"vehicle_capacity"`
		IsInstituteVehicle bool   `json:"is_institute_vehicle"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Name == "" || req.Address == "" {
		h.handleValidationError(w, "Name and address are required")
		return
	}

	if req.VehicleCapacity <= 0 {
		h.handleValidationError(w, "Vehicle capacity must be greater than 0")
		return
	}

	if req.IsInstituteVehicle && !existing.IsInstituteVehicle {
		instituteVehicle, err := h.DB.DriverRepository.GetInstituteVehicle(r.Context())
		if err != nil {
			h.handleInternalError(w, err)
			return
		}
		if instituteVehicle != nil && instituteVehicle.ID != id {
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
			h.handleGeocodingError(w, err)
			return
		}
		driver.Lat = geocodeResult.Coords.Lat
		driver.Lng = geocodeResult.Coords.Lng
	}

	driver, err = h.DB.DriverRepository.Update(r.Context(), driver)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			h.handleConflict(w, "Institute vehicle already exists")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if driver == nil {
		h.handleNotFound(w, "Driver not found")
		return
	}

	h.writeJSON(w, http.StatusOK, driver)
}

// HandleDeleteDriver handles DELETE /api/v1/drivers/{id}
func (h *Handler) HandleDeleteDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.handleValidationError(w, "Invalid driver ID")
		return
	}

	err = h.DB.DriverRepository.Delete(r.Context(), id)
	if h.checkNotFound(err) {
		h.handleNotFound(w, "Driver not found")
		return
	}
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
