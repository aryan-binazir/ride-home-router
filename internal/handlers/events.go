package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"ride-home-router/internal/eventsnapshot"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routefeedback"
	"ride-home-router/internal/routesession"
	"strconv"
	"strings"
	"time"
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

type eventValidationError struct {
	message string
	cause   error
}

func (e eventValidationError) Error() string {
	return e.message
}

func (e eventValidationError) Unwrap() error {
	return e.cause
}

func (h *Handler) handleEventValidationError(w http.ResponseWriter, err error) bool {
	var validationErr eventValidationError
	if !errors.As(err, &validationErr) {
		return false
	}
	h.handleValidationError(w, validationErr.message)
	return true
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
	DriverName        string           `json:"driver_name"`
	DriverAddress     string           `json:"driver_address"`
	DriverAddressName string           `json:"driver_address_name,omitempty"`
	OrgVehicleID      int64            `json:"org_vehicle_id,omitempty"`
	OrgVehicleName    string           `json:"org_vehicle_name,omitempty"`
	Stops             []AssignmentStop `json:"stops"`
}

// AssignmentStop represents a single saved stop in legacy-compatible responses.
type AssignmentStop struct {
	RouteOrder             int     `json:"route_order"`
	ParticipantName        string  `json:"participant_name"`
	ParticipantAddress     string  `json:"participant_address"`
	ParticipantAddressName string  `json:"participant_address_name,omitempty"`
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
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Event not found: id=%d", id)
			h.handleNotFound(w, messageEventNotFound)
			return
		}
		log.Printf("[ERROR] Failed to get event: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
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
	var formRoutesJSON string

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
		formRoutesJSON = r.FormValue("routes_json")

		if req.SessionID == "" {
			routes, ok := h.parsePostedRoutesJSON(w, formRoutesJSON)
			if !ok {
				return
			}
			req.Routes = routes
		}
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
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

	var createdEvent *models.Event
	var savedRouteCount int
	persist := func(ctx context.Context, result models.RoutingResult) error {
		eventDate, err := time.Parse("2006-01-02", req.EventDate)
		if err != nil {
			log.Printf("[HTTP] POST /api/v1/events: invalid_date=%s err=%v", req.EventDate, err)
			return eventValidationError{message: messageInvalidEventDateFormat, cause: err}
		}

		snapshot, err := eventsnapshot.Build(result)
		if err != nil {
			log.Printf("[HTTP] POST /api/v1/events: invalid_routes err=%v", err)
			message := err.Error()
			if errors.Is(err, models.ErrInvalidRouteMode) {
				message = messageInvalidRouteMode
			}
			return eventValidationError{message: message, cause: err}
		}

		event := &models.Event{EventDate: eventDate, Notes: req.Notes, Mode: snapshot.Mode}
		created, err := h.DB.Events().Create(ctx, event, snapshot.Routes, &snapshot.Summary)
		if err != nil {
			log.Printf("[ERROR] Failed to create event: date=%s routes=%d err=%v", req.EventDate, len(snapshot.Routes), err)
			return err
		}
		createdEvent = created
		savedRouteCount = len(snapshot.Routes)
		return nil
	}

	if req.SessionID != "" {
		sessionErr := h.RouteSession.Commit(r.Context(), req.SessionID, func(ctx context.Context, snapshot routesession.CommitSnapshot) error {
			if err := persist(ctx, snapshot.RoutingResult()); err != nil {
				return err
			}
			if strings.TrimSpace(r.Header.Get(routefeedback.AuthenticatedUserEmailHeader)) == "" {
				return nil
			}
			feedbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			settings, err := h.DB.Settings().Get(feedbackCtx)
			if err != nil {
				log.Printf("[FEEDBACK] settings read failed event_id=%d session_id=%s err=%v", createdEvent.ID, snapshot.SessionID, err)
				return nil
			}
			email, ok := routefeedback.ShouldCapture(r, settings)
			if !ok {
				return nil
			}
			record := routefeedback.Build(snapshot)
			record.EventID = createdEvent.ID
			record.SMEEmail = email
			if err := h.DB.RouteFeedback().Create(feedbackCtx, &record); err != nil {
				log.Printf("[FEEDBACK] create failed event_id=%d session_id=%s err=%v", createdEvent.ID, snapshot.SessionID, err)
			}
			return nil
		})
		if h.handleEventValidationError(w, sessionErr) {
			return
		}
		if errors.Is(sessionErr, routesession.ErrAlreadyCommitted) {
			log.Printf("[HTTP] POST /api/v1/events: session_already_committed session_id=%s", req.SessionID)
			h.handleNotFound(w, messageSessionNotFound)
			return
		} else if errors.Is(sessionErr, routesession.ErrNotFound) {
			if req.Routes == nil && formRoutesJSON != "" {
				routes, ok := h.parsePostedRoutesJSON(w, formRoutesJSON)
				if !ok {
					return
				}
				req.Routes = routes
			}
			if req.Routes == nil {
				log.Printf("[HTTP] POST /api/v1/events: session_not_found session_id=%s", req.SessionID)
				h.handleNotFound(w, messageSessionNotFound)
				return
			}
			log.Printf("[HTTP] POST /api/v1/events: session_not_found using posted routes fallback session_id=%s", req.SessionID)
			if err := persist(r.Context(), *req.Routes); err != nil {
				if !h.handleEventValidationError(w, err) {
					h.handleInternalError(w, err)
				}
				return
			}
		} else if errors.Is(sessionErr, routesession.ErrUnbalanced) {
			log.Printf("[HTTP] POST /api/v1/events: blocked save for out-of-balance session_id=%s", req.SessionID)
			h.handleValidationError(w, messageRoutesMustBeBalancedBeforeSaving)
			return
		} else if sessionErr != nil {
			h.handleInternalError(w, sessionErr)
			return
		}
	} else if req.Routes == nil {
		log.Printf("[HTTP] POST /api/v1/events: missing routes")
		h.handleValidationError(w, messageRoutesRequired)
		return
	} else if err := persist(r.Context(), *req.Routes); err != nil {
		if !h.handleEventValidationError(w, err) {
			h.handleInternalError(w, err)
		}
		return
	}

	log.Printf("[HTTP] Created event: id=%d date=%s routes=%d", createdEvent.ID, createdEvent.EventDate.Format("2006-01-02"), savedRouteCount)

	if h.isHTMX(r) {
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `<div class="alert alert-success">Event saved successfully! <a href="/history">View History</a></div>`)
		return
	}

	h.writeJSON(w, http.StatusCreated, createdEvent)
}

func (h *Handler) parsePostedRoutesJSON(w http.ResponseWriter, routesJSON string) (*models.RoutingResult, bool) {
	if routesJSON == "" {
		return nil, true
	}
	var routingResult models.RoutingResult
	if err := json.Unmarshal([]byte(routesJSON), &routingResult); err != nil {
		log.Printf("[HTTP] POST /api/v1/events: invalid_routes_json err=%v", err)
		h.handleValidationError(w, messageInvalidRoutesData)
		return nil, false
	}
	return &routingResult, true
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

func groupRoutesByDriver(routes []models.EventRoute) []AssignmentGroupedByDriver {
	grouped := make([]AssignmentGroupedByDriver, 0, len(routes))

	for _, route := range routes {
		if len(route.Stops) == 0 {
			continue
		}

		group := AssignmentGroupedByDriver{
			DriverName:        route.DriverName,
			DriverAddress:     route.DriverAddress,
			DriverAddressName: route.DriverAddressName,
			OrgVehicleID:      route.OrgVehicleID,
			OrgVehicleName:    route.OrgVehicleName,
			Stops:             make([]AssignmentStop, 0, len(route.Stops)),
		}

		for _, stop := range route.Stops {
			group.Stops = append(group.Stops, AssignmentStop{
				RouteOrder:             stop.Order,
				ParticipantName:        stop.ParticipantName,
				ParticipantAddress:     stop.ParticipantAddress,
				ParticipantAddressName: stop.ParticipantAddressName,
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
	code := http.StatusOK

	if err := h.DB.HealthCheck(r.Context()); err != nil {
		status = "degraded"
		dbStatus = "error"
		code = http.StatusServiceUnavailable
	}

	h.writeJSON(w, code, map[string]string{
		"status":   status,
		"version":  "1.0.0",
		"database": dbStatus,
	})
}
