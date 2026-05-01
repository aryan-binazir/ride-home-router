package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ride-home-router/internal/httpx"
	"ride-home-router/internal/models"
)

// EventListResponse represents the list response.
type EventListResponse struct {
	Events []EventWithSummary `json:"events"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// EventWithSummary combines event and summary for list view.
type EventWithSummary struct {
	ID        int64                `json:"id"`
	EventDate time.Time            `json:"event_date"`
	Notes     string               `json:"notes"`
	CreatedAt time.Time            `json:"created_at"`
	Summary   *models.EventSummary `json:"summary,omitempty"`
}

// EventListViewData backs the history partial and page.
type EventListViewData struct {
	Events         []EventWithSummary `json:"events"`
	Total          int                `json:"total"`
	Limit          int                `json:"limit"`
	Offset         int                `json:"offset"`
	DisplayedCount int                `json:"displayed_count"`
	NextOffset     int                `json:"next_offset"`
	PageSize       int                `json:"page_size"`
	UseMiles       bool               `json:"use_miles"`
}

const defaultEventListPageSize = 20

// CreateEventRequest represents the request to create an event.
type CreateEventRequest struct {
	EventDate string                `json:"event_date"`
	Notes     string                `json:"notes"`
	Routes    *models.RoutingResult `json:"routes"`
	SessionID string                `json:"session_id"`
}

// EventDetailResponse represents the detailed event response.
type EventDetailResponse struct {
	ID          int64                       `json:"id"`
	EventDate   time.Time                   `json:"event_date"`
	Notes       string                      `json:"notes"`
	CreatedAt   time.Time                   `json:"created_at"`
	Assignments []AssignmentGroupedByDriver `json:"assignments"`
	Summary     *models.EventSummary        `json:"summary"`
}

// AssignmentGroupedByDriver groups stops by driver for legacy-compatible responses.
type AssignmentGroupedByDriver struct {
	DriverName     string           `json:"driver_name"`
	DriverAddress  string           `json:"driver_address"`
	OrgVehicleID   int64            `json:"org_vehicle_id,omitempty"`
	OrgVehicleName string           `json:"org_vehicle_name,omitempty"`
	Stops          []AssignmentStop `json:"stops"`
}

// AssignmentStop represents a single saved stop in legacy-compatible responses.
type AssignmentStop struct {
	RouteOrder             int     `json:"route_order"`
	ParticipantName        string  `json:"participant_name"`
	ParticipantAddress     string  `json:"participant_address"`
	DistanceFromPrevMeters float64 `json:"distance_from_prev_meters"`
}

// EventDetailViewData backs the history detail partial.
type EventDetailViewData struct {
	Routes               []models.EventRoute         `json:"routes"`
	Assignments          []AssignmentGroupedByDriver `json:"assignments"`
	Summary              *models.EventSummary        `json:"summary"`
	UseMiles             bool                        `json:"use_miles"`
	UseLegacyAssignments bool                        `json:"use_legacy_assignments"`
}

// HandleListEvents handles GET /api/v1/events.
func (h *Handler) HandleListEvents(w http.ResponseWriter, r *http.Request) {
	limit := defaultEventListPageSize
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

	view, err := h.buildEventListView(r.Context(), limit, offset)
	if err != nil {
		log.Printf("[ERROR] Failed to build event list view: limit=%d offset=%d err=%v", limit, offset, err)
		h.handleInternalError(w, err)
		return
	}

	if h.isHTMX(r) {
		if offset > 0 {
			h.renderTemplate(w, "event_list_page", view)
			return
		}
		h.renderTemplate(w, "event_list", view)
		return
	}

	h.writeJSON(w, http.StatusOK, EventListResponse{
		Events: view.Events,
		Total:  view.Total,
		Limit:  limit,
		Offset: offset,
	})
}

// HandleGetEvent handles GET /api/v1/events/{id}.
func (h *Handler) HandleGetEvent(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] GET /api/v1/events/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, messageInvalidEventID)
		return
	}

	log.Printf("[HTTP] GET /api/v1/events/{id}: id=%d", id)
	event, routes, summary, err := h.DB.Events().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get event: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	if event == nil {
		log.Printf("[HTTP] Event not found: id=%d", id)
		h.handleNotFound(w, messageEventNotFound)
		return
	}

	assignments := groupRoutesByDriver(routes)

	if h.isHTMX(r) {
		settings, err := h.DB.Settings().Get(r.Context())
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "event_detail", EventDetailViewData{
			Routes:               routes,
			Assignments:          assignments,
			Summary:              summary,
			UseMiles:             settings.UseMiles,
			UseLegacyAssignments: routesNeedLegacyDetail(routes),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, EventDetailResponse{
		ID:          event.ID,
		EventDate:   event.EventDate,
		Notes:       event.Notes,
		CreatedAt:   event.CreatedAt,
		Assignments: assignments,
		Summary:     summary,
	})
}

// HandleCreateEvent handles POST /api/v1/events.
func (h *Handler) HandleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var req CreateEventRequest

	contentType := r.Header.Get(httpx.HeaderContentType)
	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/events: form_parse_error err=%v", err)
			h.handleValidationError(w, messageInvalidFormData)
			return
		}

		req.EventDate = r.FormValue("event_date")
		req.Notes = r.FormValue("notes")
		req.SessionID = r.FormValue("session_id")

		routesJSON := r.FormValue("routes_json")
		if routesJSON != "" {
			var routingResult models.RoutingResult
			if err := json.Unmarshal([]byte(routesJSON), &routingResult); err != nil {
				log.Printf("[HTTP] POST /api/v1/events: invalid_routes_json err=%v", err)
				h.handleValidationError(w, messageInvalidRoutesData)
				return
			}
			req.Routes = &routingResult
		}
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/events: invalid_body err=%v", err)
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
	}

	if req.EventDate == "" {
		log.Printf("[HTTP] POST /api/v1/events: missing event_date")
		h.handleValidationError(w, messageEventDateRequired)
		return
	}
	if req.Routes == nil {
		log.Printf("[HTTP] POST /api/v1/events: missing routes")
		h.handleValidationError(w, messageRoutesRequired)
		return
	}
	if req.SessionID != "" {
		session := h.RouteSession.Get(req.SessionID)
		if session != nil {
			session.mu.Lock()
			_, isOutOfBalance := calculateOverCapacity(session.CurrentRoutes)
			session.mu.Unlock()
			if isOutOfBalance {
				log.Printf("[HTTP] POST /api/v1/events: blocked save for out-of-balance session_id=%s", req.SessionID)
				h.handleValidationError(w, messageRoutesMustBeBalancedBeforeSaving)
				return
			}
		}
	}

	eventDate, err := time.Parse("2006-01-02", req.EventDate)
	if err != nil {
		log.Printf("[HTTP] POST /api/v1/events: invalid_date=%s err=%v", req.EventDate, err)
		h.handleValidationError(w, messageInvalidEventDateFormat)
		return
	}

	mode, routes, summary, err := buildEventSnapshots(req.Routes)
	if err != nil {
		log.Printf("[HTTP] POST /api/v1/events: invalid_routes err=%v", err)
		h.handleValidationError(w, err.Error())
		return
	}

	event := &models.Event{
		EventDate: eventDate,
		Notes:     req.Notes,
		Mode:      mode,
	}

	event, err = h.DB.Events().Create(r.Context(), event, routes, summary)
	if err != nil {
		log.Printf("[ERROR] Failed to create event: date=%s routes=%d err=%v", req.EventDate, len(routes), err)
		h.handleInternalError(w, err)
		return
	}

	if req.SessionID != "" {
		h.RouteSession.Delete(req.SessionID)
	}

	log.Printf("[HTTP] Created event: id=%d date=%s routes=%d", event.ID, event.EventDate.Format("2006-01-02"), len(routes))

	if h.isHTMX(r) {
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `<div class="alert alert-success">Event saved successfully! <a href="/history">View History</a></div>`)
		return
	}

	h.writeJSON(w, http.StatusCreated, event)
}

// HandleDeleteEvent handles DELETE /api/v1/events/{id}.
func (h *Handler) HandleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/events/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/events/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, messageInvalidEventID)
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/events/{id}: id=%d", id)
	err = h.DB.Events().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Event not found for delete: id=%d", id)
		h.handleNotFound(w, messageEventNotFound)
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete event: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted event: id=%d", id)

	if h.isHTMX(r) {
		limit := defaultEventListPageSize
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
				limit = l
			}
		}

		view, err := h.buildEventListView(r.Context(), limit, 0)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "event_list", view)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) buildEventListView(ctx context.Context, limit, offset int) (*EventListViewData, error) {
	events, total, err := h.DB.Events().List(ctx, limit, offset)
	if err != nil {
		return nil, err
	}

	eventIDs := make([]int64, len(events))
	for i, event := range events {
		eventIDs[i] = event.ID
	}

	summariesByEventID, err := h.DB.Events().GetSummariesByEventIDs(ctx, eventIDs)
	if err != nil {
		return nil, err
	}

	eventsWithSummary := make([]EventWithSummary, len(events))
	for i, event := range events {
		eventsWithSummary[i] = EventWithSummary{
			ID:        event.ID,
			EventDate: event.EventDate,
			Notes:     event.Notes,
			CreatedAt: event.CreatedAt,
			Summary:   summariesByEventID[event.ID],
		}
	}

	settings, err := h.DB.Settings().Get(ctx)
	if err != nil {
		return nil, err
	}

	displayedCount := offset + len(events)

	return &EventListViewData{
		Events:         eventsWithSummary,
		Total:          total,
		Limit:          limit,
		Offset:         offset,
		DisplayedCount: displayedCount,
		NextOffset:     displayedCount,
		PageSize:       defaultEventListPageSize,
		UseMiles:       settings.UseMiles,
	}, nil
}

func buildEventSnapshots(result *models.RoutingResult) (models.RouteMode, []models.EventRoute, *models.EventSummary, error) {
	if result == nil {
		return "", nil, nil, fmt.Errorf("routes are required")
	}

	mode, err := normalizeRouteMode(string(result.Mode))
	if err != nil {
		return "", nil, nil, err
	}

	routes := make([]models.EventRoute, 0, len(result.Routes))
	totalParticipants := 0
	totalDrivers := 0
	totalDistance := 0.0
	orgVehiclesUsed := 0

	for _, route := range result.Routes {
		if route.Driver == nil {
			return "", nil, nil, fmt.Errorf("each route must include a driver")
		}
		if len(route.Stops) == 0 {
			continue
		}

		routeMode := route.Mode
		if routeMode == "" {
			routeMode = mode
		}
		routeMode, err = normalizeRouteMode(string(routeMode))
		if err != nil {
			return "", nil, nil, err
		}
		if routeMode != mode {
			return "", nil, nil, fmt.Errorf("all routes must use the same mode")
		}

		snapshot := models.EventRoute{
			RouteOrder:                 len(routes),
			DriverID:                   route.Driver.ID,
			DriverName:                 route.Driver.Name,
			DriverAddress:              route.Driver.Address,
			EffectiveCapacity:          route.EffectiveCapacity,
			OrgVehicleID:               route.OrgVehicleID,
			OrgVehicleName:             route.OrgVehicleName,
			TotalDropoffDistanceMeters: route.TotalDropoffDistanceMeters,
			DistanceToDriverHomeMeters: route.DistanceToDriverHomeMeters,
			TotalDistanceMeters:        route.TotalDistanceMeters,
			BaselineDurationSecs:       route.BaselineDurationSecs,
			RouteDurationSecs:          route.RouteDurationSecs,
			DetourSecs:                 route.DetourSecs,
			Mode:                       routeMode,
			SnapshotVersion:            2,
			MetricsComplete:            true,
			Stops:                      make([]models.EventRouteStop, 0, len(route.Stops)),
		}
		if snapshot.EffectiveCapacity == 0 {
			snapshot.EffectiveCapacity = route.Driver.VehicleCapacity
		}

		for stopIndex, stop := range route.Stops {
			if stop.Participant == nil {
				return "", nil, nil, fmt.Errorf("each route stop must include a participant")
			}
			snapshot.Stops = append(snapshot.Stops, models.EventRouteStop{
				Order:                    stopIndex,
				ParticipantID:            stop.Participant.ID,
				ParticipantName:          stop.Participant.Name,
				ParticipantAddress:       stop.Participant.Address,
				DistanceFromPrevMeters:   stop.DistanceFromPrevMeters,
				CumulativeDistanceMeters: stop.CumulativeDistanceMeters,
				DurationFromPrevSecs:     stop.DurationFromPrevSecs,
				CumulativeDurationSecs:   stop.CumulativeDurationSecs,
			})
			totalParticipants++
		}

		totalDrivers++
		totalDistance += route.TotalDistanceMeters
		if route.OrgVehicleID > 0 {
			orgVehiclesUsed++
		}
		routes = append(routes, snapshot)
	}

	if len(routes) == 0 {
		return "", nil, nil, fmt.Errorf("routes are required")
	}

	return mode, routes, &models.EventSummary{
		TotalParticipants:   totalParticipants,
		TotalDrivers:        totalDrivers,
		TotalDistanceMeters: totalDistance,
		OrgVehiclesUsed:     orgVehiclesUsed,
		Mode:                mode,
	}, nil
}

func groupRoutesByDriver(routes []models.EventRoute) []AssignmentGroupedByDriver {
	grouped := make([]AssignmentGroupedByDriver, 0, len(routes))

	for _, route := range routes {
		if len(route.Stops) == 0 {
			continue
		}

		group := AssignmentGroupedByDriver{
			DriverName:     route.DriverName,
			DriverAddress:  route.DriverAddress,
			OrgVehicleID:   route.OrgVehicleID,
			OrgVehicleName: route.OrgVehicleName,
			Stops:          make([]AssignmentStop, 0, len(route.Stops)),
		}

		for _, stop := range route.Stops {
			group.Stops = append(group.Stops, AssignmentStop{
				RouteOrder:             stop.Order,
				ParticipantName:        stop.ParticipantName,
				ParticipantAddress:     stop.ParticipantAddress,
				DistanceFromPrevMeters: stop.DistanceFromPrevMeters,
			})
		}

		grouped = append(grouped, group)
	}

	return grouped
}

func routesNeedLegacyDetail(routes []models.EventRoute) bool {
	for _, route := range routes {
		if !route.MetricsComplete || route.SnapshotVersion <= 1 {
			return true
		}
	}
	return false
}

// HandleHealthCheck handles GET /api/v1/health.
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
