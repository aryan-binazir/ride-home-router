package handlers

import (
	"encoding/json"
	"fmt"
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
	DriverName     string           `json:"driver_name"`
	DriverAddress  string           `json:"driver_address"`
	OrgVehicleID   int64            `json:"org_vehicle_id,omitempty"`
	OrgVehicleName string           `json:"org_vehicle_name,omitempty"`
	Stops          []AssignmentStop `json:"stops"`
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
	events, total, err := h.DB.Events().List(r.Context(), limit, offset)
	if err != nil {
		log.Printf("[ERROR] Failed to list events: limit=%d offset=%d err=%v", limit, offset, err)
		h.handleInternalError(w, err)
		return
	}

	eventsWithSummary := make([]EventWithSummary, len(events))
	for i, event := range events {
		_, _, summary, err := h.DB.Events().GetByID(r.Context(), event.ID)
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
	event, assignments, summary, err := h.DB.Events().GetByID(r.Context(), id)
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

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		settings, err := h.DB.Settings().Get(r.Context())
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "event_detail", map[string]interface{}{
			"Assignments": assignments,
			"Summary":     summary,
			"UseMiles":    settings.UseMiles,
		})
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

	contentType := r.Header.Get("Content-Type")

	// Handle form data (from htmx)
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/events: form_parse_error err=%v", err)
			h.handleValidationError(w, "Invalid form data")
			return
		}

		req.EventDate = r.FormValue("event_date")
		req.Notes = r.FormValue("notes")

		// Parse routes_json from form
		routesJSON := r.FormValue("routes_json")
		if routesJSON != "" {
			var routingResult models.RoutingResult
			if err := json.Unmarshal([]byte(routesJSON), &routingResult); err != nil {
				log.Printf("[HTTP] POST /api/v1/events: invalid_routes_json err=%v", err)
				h.handleValidationError(w, "Invalid routes data")
				return
			}
			req.Routes = &routingResult
		}

		log.Printf("[HTTP] POST /api/v1/events: form_data event_date=%s routes_count=%d", req.EventDate, len(req.Routes.Routes))
	} else {
		// Handle JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/events: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
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
		Mode:      req.Routes.Mode,
	}

	var assignments []models.EventAssignment
	orgVehiclesUsed := 0
	for _, route := range req.Routes.Routes {
		if route.OrgVehicleID > 0 {
			orgVehiclesUsed++
		}
		for _, stop := range route.Stops {
			assignments = append(assignments, models.EventAssignment{
				DriverID:           route.Driver.ID,
				DriverName:         route.Driver.Name,
				DriverAddress:      route.Driver.Address,
				RouteOrder:         stop.Order,
				ParticipantID:      stop.Participant.ID,
				ParticipantName:    stop.Participant.Name,
				ParticipantAddress: stop.Participant.Address,
				DistanceFromPrev:   stop.DistanceFromPrevMeters,
				OrgVehicleID:       route.OrgVehicleID,
				OrgVehicleName:     route.OrgVehicleName,
			})
		}
	}

	summary := &models.EventSummary{
		TotalParticipants:   req.Routes.Summary.TotalParticipants,
		TotalDrivers:        req.Routes.Summary.TotalDriversUsed,
		TotalDistanceMeters: req.Routes.Summary.TotalDropoffDistanceMeters,
		OrgVehiclesUsed:     orgVehiclesUsed,
		Mode:                req.Routes.Mode,
	}

	event, err = h.DB.Events().Create(r.Context(), event, assignments, summary)
	if err != nil {
		log.Printf("[ERROR] Failed to create event: date=%s participants=%d err=%v", req.EventDate, len(assignments), err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created event: id=%d date=%s assignments=%d", event.ID, event.EventDate.Format("2006-01-02"), len(assignments))

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `<div class="alert alert-success">Event saved successfully! <a href="/history">View History</a></div>`)
		return
	}

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
	err = h.DB.Events().Delete(r.Context(), id)
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

	// Return refreshed list for htmx, 204 for API
	if h.isHTMX(r) {
		events, total, err := h.DB.Events().List(r.Context(), 20, 0)
		if err != nil {
			h.renderError(w, r, err)
			return
		}

		eventsWithSummary := make([]EventWithSummary, len(events))
		for i, event := range events {
			_, _, summary, err := h.DB.Events().GetByID(r.Context(), event.ID)
			if err != nil {
				h.renderError(w, r, err)
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

		h.renderTemplate(w, "event_list", map[string]interface{}{
			"Events": eventsWithSummary,
			"Total":  total,
		})
		return
	}

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
				DriverName:     a.DriverName,
				DriverAddress:  a.DriverAddress,
				OrgVehicleID:   a.OrgVehicleID,
				OrgVehicleName: a.OrgVehicleName,
				Stops:          []AssignmentStop{},
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
