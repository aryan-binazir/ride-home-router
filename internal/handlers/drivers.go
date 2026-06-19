package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

// DriverListResponse represents the list response
type DriverListResponse struct {
	Drivers []DriverResponse `json:"drivers"`
	Total   int              `json:"total"`
}

// DriverResponse represents a driver API response.
type DriverResponse struct {
	models.Driver
	LabelIDs []int64 `json:"label_ids"`
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
		view, err := h.driverListView(r, drivers)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "driver_list", view)
		return
	}

	responseDrivers, err := h.driverResponses(r.Context(), drivers)
	if err != nil {
		log.Printf("[ERROR] Failed to load driver labels for list: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, DriverListResponse{
		Drivers: responseDrivers,
		Total:   len(drivers),
	})
}

// HandleGetDriver handles GET /api/v1/drivers/{id}
func (h *Handler) HandleGetDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, messageInvalidDriverID)
		return
	}

	log.Printf("[HTTP] GET /api/v1/drivers/{id}: id=%d", id)
	driver, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Driver not found: id=%d", id)
			h.handleNotFound(w, messageDriverNotFound)
			return
		}
		log.Printf("[ERROR] Failed to get driver: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	response, err := h.driverResponse(r.Context(), driver)
	if err != nil {
		log.Printf("[ERROR] Failed to load driver labels: id=%d err=%v", driver.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleCreateDriver handles POST /api/v1/drivers
func (h *Handler) HandleCreateDriver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string  `json:"name"`
		Address         string  `json:"address"`
		VehicleCapacity int     `json:"vehicle_capacity"`
		LabelIDs        []int64 `json:"label_ids"`
	}
	var labelIDs []int64

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
				h.renderError(w, r, errors.New("invalid vehicle capacity"))
				return
			}
			req.VehicleCapacity = capacity
		}
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.renderError(w, r, errors.New("invalid label selection"))
			return
		}
		labelIDs = parsedLabelIDs
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		labelIDs = req.LabelIDs
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageNameAndAddressRequired))
			return
		}
		h.handleValidationError(w, messageNameAndAddressRequired)
		return
	}

	if req.VehicleCapacity <= 0 {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageVehicleCapacityMustBeGreaterThanZero))
			return
		}
		h.handleValidationError(w, messageVehicleCapacityMustBeGreaterThanZero)
		return
	}
	if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
		log.Printf("[HTTP] POST /api/v1/drivers: invalid_labels err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidLabelSelection))
			return
		}
		h.handleValidationError(w, messageInvalidLabelSelection)
		return
	}

	log.Printf("[HTTP] POST /api/v1/drivers: name=%s address=%s capacity=%d", req.Name, req.Address, req.VehicleCapacity)
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
		Name:            req.Name,
		Address:         req.Address,
		Lat:             geocodeResult.Coords.Lat,
		Lng:             geocodeResult.Coords.Lng,
		VehicleCapacity: req.VehicleCapacity,
	}

	driver, err = h.DB.Drivers().CreateWithLabels(r.Context(), driver, labelIDs)
	if err != nil {
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
		h.setHTMXToastWithEvent(w, "driverCreated", messageEntityAdded("Driver", driver.Name), toastTypeSuccess)
		view, err := h.driverListView(r, drivers)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "driver_list", view)
		return
	}

	response, err := h.driverResponse(r.Context(), driver)
	if err != nil {
		log.Printf("[ERROR] Failed to load driver labels after create: id=%d err=%v", driver.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, response)
}

// HandleUpdateDriver handles PUT /api/v1/drivers/{id}
func (h *Handler) HandleUpdateDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	if trimmedID, ok := strings.CutSuffix(idStr, "/edit"); ok {
		idStr = trimmedID
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidDriverID))
			return
		}
		h.handleValidationError(w, messageInvalidDriverID)
		return
	}

	log.Printf("[HTTP] PUT /api/v1/drivers/{id}: id=%d", id)
	existing, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageDriverNotFound))
				return
			}
			h.handleNotFound(w, messageDriverNotFound)
			return
		}
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	var req struct {
		Name            string   `json:"name"`
		Address         string   `json:"address"`
		VehicleCapacity int      `json:"vehicle_capacity"`
		LabelIDs        *[]int64 `json:"label_ids"`
	}
	var labelIDs []int64
	shouldSetLabels := false

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
				h.renderError(w, r, errors.New("invalid vehicle capacity"))
				return
			}
			req.VehicleCapacity = capacity
		}
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.renderError(w, r, errors.New("invalid label selection"))
			return
		}
		labelIDs = parsedLabelIDs
		shouldSetLabels = true
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		if req.LabelIDs != nil {
			labelIDs = *req.LabelIDs
			shouldSetLabels = true
		}
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageNameAndAddressRequired))
			return
		}
		h.handleValidationError(w, messageNameAndAddressRequired)
		return
	}

	if req.VehicleCapacity <= 0 {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageVehicleCapacityMustBeGreaterThanZero))
			return
		}
		h.handleValidationError(w, messageVehicleCapacityMustBeGreaterThanZero)
		return
	}
	if shouldSetLabels {
		if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
			log.Printf("[HTTP] PUT /api/v1/drivers/{id}: invalid_labels id=%d err=%v", id, err)
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageInvalidLabelSelection))
				return
			}
			h.handleValidationError(w, messageInvalidLabelSelection)
			return
		}
	}

	driver := &models.Driver{
		ID:              id,
		Name:            req.Name,
		Address:         req.Address,
		Lat:             existing.Lat,
		Lng:             existing.Lng,
		VehicleCapacity: req.VehicleCapacity,
		CreatedAt:       existing.CreatedAt,
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

	if shouldSetLabels {
		driver, err = h.DB.Drivers().UpdateWithLabels(r.Context(), driver, labelIDs)
	} else {
		driver, err = h.DB.Drivers().Update(r.Context(), driver)
	}
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Driver not found after update: id=%d", id)
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageDriverNotFound))
				return
			}
			h.handleNotFound(w, messageDriverNotFound)
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

	log.Printf("[HTTP] Updated driver: id=%d name=%s", driver.ID, driver.Name)
	if h.isHTMX(r) {
		drivers, err := h.DB.Drivers().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list drivers after update: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		h.setHTMXToastWithEvent(w, "driverUpdated", messageEntityUpdated("Driver", driver.Name), toastTypeSuccess)
		view, err := h.driverListView(r, drivers)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "driver_list", view)
		return
	}

	response, err := h.driverResponse(r.Context(), driver)
	if err != nil {
		log.Printf("[ERROR] Failed to load driver labels after update: id=%d err=%v", driver.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleDeleteDriver handles DELETE /api/v1/drivers/{id}
func (h *Handler) HandleDeleteDriver(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/drivers/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/drivers/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidDriverID))
			return
		}
		h.handleValidationError(w, messageInvalidDriverID)
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/drivers/{id}: id=%d", id)
	err = h.DB.Drivers().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Driver not found for delete: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageDriverNotFound))
			return
		}
		h.handleNotFound(w, messageDriverNotFound)
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
		h.setHTMXToast(w, messageEntityDeleted("Driver"), toastTypeSuccess)
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
	var (
		labels           []models.Label
		selectedLabelIDs map[int64]bool
		err              error
	)
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.renderError(w, r, errors.New(messageInvalidDriverID))
			return
		}

		driver, err = h.DB.Drivers().GetByID(r.Context(), id)
		if err != nil {
			if h.checkNotFound(err) {
				h.renderError(w, r, errors.New(messageDriverNotFound))
				return
			}
			h.renderError(w, r, err)
			return
		}
		labels, selectedLabelIDs, err = h.loadLabelsForDriver(r, driver.ID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
	} else {
		driver = &models.Driver{}
		labels, err = h.DB.Labels().List(r.Context())
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		selectedLabelIDs = map[int64]bool{}
	}

	h.renderTemplate(w, "driver_form", DriverFormView{
		Driver:           driver,
		Labels:           labels,
		SelectedLabelIDs: selectedLabelIDs,
	})
}

func (h *Handler) driverListView(r *http.Request, drivers []models.Driver) (DriverListView, error) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		return DriverListView{}, err
	}
	labelIDs, err := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		return DriverListView{}, err
	}
	return DriverListView{
		Drivers:  drivers,
		Labels:   labels,
		LabelIDs: labelIDs,
	}, nil
}

func (h *Handler) loadLabelsForDriver(r *http.Request, driverID int64) ([]models.Label, map[int64]bool, error) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		return nil, nil, err
	}
	selectedLabels, err := h.DB.Labels().ListLabelsForDriver(r.Context(), driverID)
	if err != nil {
		return nil, nil, err
	}
	return labels, buildSelectedLabelIDMap(selectedLabels), nil
}

func (h *Handler) driverResponse(ctx context.Context, driver *models.Driver) (DriverResponse, error) {
	labels, err := h.DB.Labels().ListLabelsForDriver(ctx, driver.ID)
	if err != nil {
		return DriverResponse{}, err
	}
	labelIDs := make([]int64, 0, len(labels))
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
	}
	return DriverResponse{
		Driver:   *driver,
		LabelIDs: labelIDs,
	}, nil
}

func (h *Handler) driverResponses(ctx context.Context, drivers []models.Driver) ([]DriverResponse, error) {
	labelIDsByDriver, err := h.DB.Labels().ListLabelIDsForDrivers(ctx)
	if err != nil {
		return nil, err
	}

	responses := make([]DriverResponse, 0, len(drivers))
	for _, driver := range drivers {
		labelIDs := append([]int64{}, labelIDsByDriver[driver.ID]...)
		if labelIDs == nil {
			labelIDs = []int64{}
		}
		responses = append(responses, DriverResponse{
			Driver:   driver,
			LabelIDs: labelIDs,
		})
	}
	return responses, nil
}
