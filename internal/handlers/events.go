package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ride-home-router/internal/models"
)

// EventListResponse represents the list response
type EventListResponse struct {
	Events []EventWithSummary `json:"events"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// EventWithSummary combines event and summary for list view
type EventWithSummary struct {
	ID        int64                `json:"id"`
	EventDate time.Time            `json:"event_date"`
	Notes     string               `json:"notes"`
	CreatedAt time.Time            `json:"created_at"`
	Summary   *models.EventSummary `json:"summary,omitempty"`
}

// CreateEventRequest represents the request to create an event
type CreateEventRequest struct {
	EventDate string                `json:"event_date"`
	Notes     string                `json:"notes"`
	Routes    *models.RoutingResult `json:"routes"`
}

// EventDetailResponse represents the detailed event response
type EventDetailResponse struct {
	ID          int64                     `json:"id"`
	EventDate   time.Time                 `json:"event_date"`
	Notes       string                    `json:"notes"`
	CreatedAt   time.Time                 `json:"created_at"`
	Assignments []AssignmentGroupedByDriver `json:"assignments"`
	Summary     *models.EventSummary      `json:"summary"`
}

// AssignmentGroupedByDriver groups stops by driver
type AssignmentGroupedByDriver struct {
	DriverName           string              `json:"driver_name"`
	DriverAddress        string              `json:"driver_address"`
	UsedInstituteVehicle bool                `json:"used_institute_vehicle"`
	Stops                []AssignmentStop    `json:"stops"`
}

// AssignmentStop represents a single stop in an assignment
type AssignmentStop struct {
	RouteOrder           int     `json:"route_order"`
	ParticipantName      string  `json:"participant_name"`
	ParticipantAddress   string  `json:"participant_address"`
	DistanceFromPrevMeters float64 `json:"distance_from_prev_meters"`
}

// HandleListEvents handles GET /api/v1/events
func (h *Handler) HandleListEvents(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	log.Printf("[HTTP] GET /api/v1/events: limit=%d offset=%d", limit, offset)
	events, total, err := h.DB.EventRepository.List(r.Context(), limit, offset)
	if err != nil {
		log.Printf("[ERROR] Failed to list events: limit=%d offset=%d err=%v", limit, offset, err)
		h.handleInternalError(w, err)
		return
	}

	eventsWithSummary := make([]EventWithSummary, len(events))
	for i, event := range events {
		_, _, summary, err := h.DB.EventRepository.GetByID(r.Context(), event.ID)
		if err != nil {
			h.handleInternalError(w, err)
			return
		}

		eventsWithSummary[i] = EventWithSummary{
			ID:        event.ID,
			EventDate: event.EventDate,
			Notes:     event.Notes,
			CreatedAt: event.CreatedAt,
			Summary:   summary,
		}
	}

	h.writeJSON(w, http.StatusOK, EventListResponse{
		Events: eventsWithSummary,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// HandleGetEvent handles GET /api/v1/events/{id}
func (h *Handler) HandleGetEvent(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/events/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, "Invalid event ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/events/{id}: id=%d", id)
	event, assignments, summary, err := h.DB.EventRepository.GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get event: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	if event == nil {
		log.Printf("[HTTP] Event not found: id=%d", id)
		h.handleNotFound(w, "Event not found")
		return
	}

	grouped := groupAssignmentsByDriver(assignments)

	response := EventDetailResponse{
		ID:          event.ID,
		EventDate:   event.EventDate,
		Notes:       event.Notes,
		CreatedAt:   event.CreatedAt,
		Assignments: grouped,
		Summary:     summary,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleCreateEvent handles POST /api/v1/events
func (h *Handler) HandleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var req CreateEventRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[HTTP] POST /api/v1/events: invalid_body err=%v", err)
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.EventDate == "" {
		log.Printf("[HTTP] POST /api/v1/events: missing event_date")
		h.handleValidationError(w, "Event date is required")
		return
	}

	if req.Routes == nil {
		log.Printf("[HTTP] POST /api/v1/events: missing routes")
		h.handleValidationError(w, "Routes are required")
		return
	}

	eventDate, err := time.Parse("2006-01-02", req.EventDate)
	if err != nil {
		log.Printf("[HTTP] POST /api/v1/events: invalid_date=%s err=%v", req.EventDate, err)
		h.handleValidationError(w, "Invalid event date format (use YYYY-MM-DD)")
		return
	}

	event := &models.Event{
		EventDate: eventDate,
		Notes:     req.Notes,
	}

	var assignments []models.EventAssignment
	for _, route := range req.Routes.Routes {
		for _, stop := range route.Stops {
			assignments = append(assignments, models.EventAssignment{
				DriverID:             route.Driver.ID,
				DriverName:           route.Driver.Name,
				DriverAddress:        route.Driver.Address,
				RouteOrder:           stop.Order,
				ParticipantID:        stop.Participant.ID,
				ParticipantName:      stop.Participant.Name,
				ParticipantAddress:   stop.Participant.Address,
				DistanceFromPrev:     stop.DistanceFromPrevMeters,
				UsedInstituteVehicle: route.UsedInstituteVehicle,
			})
		}
	}

	summary := &models.EventSummary{
		TotalParticipants:    req.Routes.Summary.TotalParticipants,
		TotalDrivers:         req.Routes.Summary.TotalDriversUsed,
		TotalDistanceMeters:  req.Routes.Summary.TotalDropoffDistanceMeters,
		UsedInstituteVehicle: req.Routes.Summary.UsedInstituteVehicle,
	}

	if req.Routes.Summary.UsedInstituteVehicle {
		for _, route := range req.Routes.Routes {
			if route.UsedInstituteVehicle && route.InstituteVehicleDriverID > 0 {
				driver, err := h.DB.DriverRepository.GetByID(r.Context(), route.InstituteVehicleDriverID)
				if err == nil && driver != nil {
					summary.InstituteVehicleDriverName = driver.Name
				}
				break
			}
		}
	}

	event, err = h.DB.EventRepository.Create(r.Context(), event, assignments, summary)
	if err != nil {
		log.Printf("[ERROR] Failed to create event: date=%s participants=%d err=%v", req.EventDate, len(assignments), err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created event: id=%d date=%s assignments=%d", event.ID, event.EventDate.Format("2006-01-02"), len(assignments))
	h.writeJSON(w, http.StatusCreated, event)
}

// HandleDeleteEvent handles DELETE /api/v1/events/{id}
func (h *Handler) HandleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/events/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, "Invalid event ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/events/{id}: id=%d", id)
	err = h.DB.EventRepository.Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Event not found for delete: id=%d", id)
		h.handleNotFound(w, "Event not found")
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete event: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted event: id=%d", id)
	w.WriteHeader(http.StatusNoContent)
}

// HandleHealthCheck handles GET /api/v1/health
func (h *Handler) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	dbStatus := "connected"

	if err := h.DB.HealthCheck(r.Context()); err != nil {
		status = "degraded"
		dbStatus = "error"
	}

	h.writeJSON(w, http.StatusOK, map[string]string{
		"status":   status,
		"version":  "1.0.0",
		"database": dbStatus,
	})
}

func groupAssignmentsByDriver(assignments []models.EventAssignment) []AssignmentGroupedByDriver {
	driverMap := make(map[int64]*AssignmentGroupedByDriver)
	driverOrder := []int64{}

	for _, a := range assignments {
		if _, exists := driverMap[a.DriverID]; !exists {
			driverMap[a.DriverID] = &AssignmentGroupedByDriver{
				DriverName:           a.DriverName,
				DriverAddress:        a.DriverAddress,
				UsedInstituteVehicle: a.UsedInstituteVehicle,
				Stops:                []AssignmentStop{},
			}
			driverOrder = append(driverOrder, a.DriverID)
		}

		driverMap[a.DriverID].Stops = append(driverMap[a.DriverID].Stops, AssignmentStop{
			RouteOrder:             a.RouteOrder,
			ParticipantName:        a.ParticipantName,
			ParticipantAddress:     a.ParticipantAddress,
			DistanceFromPrevMeters: a.DistanceFromPrev,
		})
	}

	result := make([]AssignmentGroupedByDriver, len(driverOrder))
	for i, driverID := range driverOrder {
		result[i] = *driverMap[driverID]
	}

	return result
}
