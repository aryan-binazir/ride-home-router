package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"ride-home-router/internal/models"
)

// RouteSession stores calculated routes for editing
type RouteSession struct {
	ID               string
	OriginalRoutes   []models.CalculatedRoute
	CurrentRoutes    []models.CalculatedRoute
	SelectedDrivers  []models.Driver
	ActivityLocation *models.ActivityLocation
	UseMiles         bool
	mu               sync.Mutex // Protects session data during modifications
}

// RouteSessionStore manages route editing sessions in memory
type RouteSessionStore struct {
	sessions map[string]*RouteSession
	mu       sync.RWMutex
}

// NewRouteSessionStore creates a new session store
func NewRouteSessionStore() *RouteSessionStore {
	return &RouteSessionStore{
		sessions: make(map[string]*RouteSession),
	}
}

func (s *RouteSessionStore) Create(routes []models.CalculatedRoute, drivers []models.Driver, activityLocation *models.ActivityLocation, useMiles bool) *RouteSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateSessionID()

	// Deep copy routes for original
	originalRoutes := deepCopyRoutes(routes)
	currentRoutes := deepCopyRoutes(routes)

	session := &RouteSession{
		ID:               id,
		OriginalRoutes:   originalRoutes,
		CurrentRoutes:    currentRoutes,
		SelectedDrivers:  drivers,
		ActivityLocation: activityLocation,
		UseMiles:         useMiles,
	}

	s.sessions[id] = session
	log.Printf("[SESSION] Created route session: id=%s routes=%d drivers=%d", id, len(routes), len(drivers))
	return session
}

func (s *RouteSessionStore) Get(id string) *RouteSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// Update executes a function on a session while holding the write lock.
// This ensures thread-safe modifications to session data.
// Returns false if the session doesn't exist.
func (s *RouteSessionStore) Update(id string, fn func(*RouteSession)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[id]
	if session == nil {
		return false
	}
	fn(session)
	return true
}

func (s *RouteSessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	log.Printf("[SESSION] Deleted route session: id=%s", id)
}

func generateSessionID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func deepCopyRoutes(routes []models.CalculatedRoute) []models.CalculatedRoute {
	result := make([]models.CalculatedRoute, len(routes))
	for i, route := range routes {
		result[i] = models.CalculatedRoute{
			Driver:                     copyDriver(route.Driver),
			Stops:                      copyStops(route.Stops),
			TotalDropoffDistanceMeters: route.TotalDropoffDistanceMeters,
			DistanceToDriverHomeMeters: route.DistanceToDriverHomeMeters,
			TotalDistanceMeters:        route.TotalDistanceMeters,
			UsedInstituteVehicle:       route.UsedInstituteVehicle,
			InstituteVehicleDriverID:   route.InstituteVehicleDriverID,
			BaselineDurationSecs:       route.BaselineDurationSecs,
			RouteDurationSecs:          route.RouteDurationSecs,
			DetourSecs:                 route.DetourSecs,
		}
	}
	return result
}

func copyDriver(d *models.Driver) *models.Driver {
	if d == nil {
		return nil
	}
	copy := *d
	return &copy
}

func copyStops(stops []models.RouteStop) []models.RouteStop {
	result := make([]models.RouteStop, len(stops))
	for i, stop := range stops {
		result[i] = models.RouteStop{
			Order:                    stop.Order,
			Participant:              copyParticipant(stop.Participant),
			DistanceFromPrevMeters:   stop.DistanceFromPrevMeters,
			CumulativeDistanceMeters: stop.CumulativeDistanceMeters,
			DurationFromPrevSecs:     stop.DurationFromPrevSecs,
			CumulativeDurationSecs:   stop.CumulativeDurationSecs,
		}
	}
	return result
}

func copyParticipant(p *models.Participant) *models.Participant {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

// HandleMoveParticipant handles POST /api/v1/routes/edit/move-participant
func (h *Handler) HandleMoveParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID         string `json:"session_id"`
		ParticipantID     int64  `json:"participant_id"`
		FromRouteIndex    int    `json:"from_route_index"`
		ToRouteIndex      int    `json:"to_route_index"`
		InsertAtPosition  int    `json:"insert_at_position"` // -1 for end
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, "Invalid request body")
		return
	}

	session := h.RouteSession.Get(req.SessionID)
	if session == nil {
		h.handleNotFoundHTMX(w, r, "Session not found")
		return
	}

	// Lock session for thread-safe modification
	session.mu.Lock()
	defer session.mu.Unlock()

	if req.FromRouteIndex < 0 || req.FromRouteIndex >= len(session.CurrentRoutes) ||
		req.ToRouteIndex < 0 || req.ToRouteIndex >= len(session.CurrentRoutes) {
		h.handleValidationErrorHTMX(w, r, "Invalid route index")
		return
	}

	fromRoute := &session.CurrentRoutes[req.FromRouteIndex]
	toRoute := &session.CurrentRoutes[req.ToRouteIndex]

	// Check capacity (applies to all vehicles including institute vehicle)
	if len(toRoute.Stops) >= toRoute.Driver.VehicleCapacity {
		h.handleValidationErrorHTMX(w, r, "Target vehicle is at capacity")
		return
	}

	// Find and remove participant from source route
	var participant *models.Participant
	var stopIdx int = -1
	for i, stop := range fromRoute.Stops {
		if stop.Participant.ID == req.ParticipantID {
			participant = stop.Participant
			stopIdx = i
			break
		}
	}

	if participant == nil {
		h.handleValidationErrorHTMX(w, r, "Participant not found in source route")
		return
	}

	// Remove from source
	fromRoute.Stops = append(fromRoute.Stops[:stopIdx], fromRoute.Stops[stopIdx+1:]...)

	// Add to destination
	newStop := models.RouteStop{
		Participant: participant,
	}

	if req.InsertAtPosition < 0 || req.InsertAtPosition >= len(toRoute.Stops) {
		toRoute.Stops = append(toRoute.Stops, newStop)
	} else {
		toRoute.Stops = append(toRoute.Stops[:req.InsertAtPosition],
			append([]models.RouteStop{newStop}, toRoute.Stops[req.InsertAtPosition:]...)...)
	}

	// Recalculate distances for both routes
	h.recalculateRouteDistances(r.Context(), session.ActivityLocation, fromRoute)
	h.recalculateRouteDistances(r.Context(), session.ActivityLocation, toRoute)

	// Recalculate summary
	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Moved participant %d from route %d to route %d",
		req.ParticipantID, req.FromRouteIndex, req.ToRouteIndex)

	// Return updated routes
	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           session.CurrentRoutes,
			"Summary":          summary,
			"UseMiles":         session.UseMiles,
			"ActivityLocation": session.ActivityLocation,
			"SessionID":        session.ID,
			"IsEditing":        true,
			"UnusedDrivers":    getUnusedDrivers(session),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}

// HandleSwapDrivers handles POST /api/v1/routes/edit/swap-drivers
func (h *Handler) HandleSwapDrivers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"session_id"`
		RouteIndex1 int    `json:"route_index_1"`
		RouteIndex2 int    `json:"route_index_2"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, "Invalid request body")
		return
	}

	session := h.RouteSession.Get(req.SessionID)
	if session == nil {
		h.handleNotFoundHTMX(w, r, "Session not found")
		return
	}

	// Lock session for thread-safe modification
	session.mu.Lock()
	defer session.mu.Unlock()

	if req.RouteIndex1 < 0 || req.RouteIndex1 >= len(session.CurrentRoutes) ||
		req.RouteIndex2 < 0 || req.RouteIndex2 >= len(session.CurrentRoutes) {
		h.handleValidationErrorHTMX(w, r, "Invalid route index")
		return
	}

	route1 := &session.CurrentRoutes[req.RouteIndex1]
	route2 := &session.CurrentRoutes[req.RouteIndex2]

	// Check capacity constraints
	if len(route1.Stops) > route2.Driver.VehicleCapacity ||
		len(route2.Stops) > route1.Driver.VehicleCapacity {
		h.handleValidationErrorHTMX(w, r, "Cannot swap - capacity constraints violated")
		return
	}

	// Swap drivers
	route1.Driver, route2.Driver = route2.Driver, route1.Driver

	// Recalculate distances for both routes (driver home changed)
	h.recalculateRouteDistances(r.Context(), session.ActivityLocation, route1)
	h.recalculateRouteDistances(r.Context(), session.ActivityLocation, route2)

	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Swapped drivers between routes %d and %d", req.RouteIndex1, req.RouteIndex2)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           session.CurrentRoutes,
			"Summary":          summary,
			"UseMiles":         session.UseMiles,
			"ActivityLocation": session.ActivityLocation,
			"SessionID":        session.ID,
			"IsEditing":        true,
			"UnusedDrivers":    getUnusedDrivers(session),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}

// HandleResetRoutes handles POST /api/v1/routes/edit/reset
func (h *Handler) HandleResetRoutes(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		// Try form value
		sessionID = r.FormValue("session_id")
	}

	session := h.RouteSession.Get(sessionID)
	if session == nil {
		h.handleNotFoundHTMX(w, r, "Session not found")
		return
	}

	// Lock session for thread-safe modification
	session.mu.Lock()
	defer session.mu.Unlock()

	// Reset to original routes
	session.CurrentRoutes = deepCopyRoutes(session.OriginalRoutes)

	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Reset routes for session %s", sessionID)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           session.CurrentRoutes,
			"Summary":          summary,
			"UseMiles":         session.UseMiles,
			"ActivityLocation": session.ActivityLocation,
			"SessionID":        session.ID,
			"IsEditing":        true,
			"UnusedDrivers":    getUnusedDrivers(session),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}

// recalculateRouteDistances recalculates distances and durations for a single route after editing
func (h *Handler) recalculateRouteDistances(ctx context.Context, activityLocation *models.ActivityLocation, route *models.CalculatedRoute) {
	if len(route.Stops) == 0 {
		route.TotalDropoffDistanceMeters = 0
		route.DistanceToDriverHomeMeters = 0
		route.TotalDistanceMeters = 0
		route.BaselineDurationSecs = 0
		route.RouteDurationSecs = 0
		route.DetourSecs = 0
		return
	}

	var totalDropoff float64
	var totalRouteDuration float64
	prevCoords := activityLocation.GetCoords()

	for i := range route.Stops {
		stop := &route.Stops[i]
		stop.Order = i

		// Calculate distance from previous point
		result, err := h.DistanceCalc.GetDistance(ctx, prevCoords, stop.Participant.GetCoords())
		if err != nil {
			log.Printf("[ERROR] Failed to calculate distance: %v", err)
			continue
		}
		stop.DistanceFromPrevMeters = result.DistanceMeters
		stop.DurationFromPrevSecs = result.DurationSecs

		if i == 0 {
			stop.CumulativeDistanceMeters = result.DistanceMeters
			stop.CumulativeDurationSecs = result.DurationSecs
		} else {
			stop.CumulativeDistanceMeters = route.Stops[i-1].CumulativeDistanceMeters + result.DistanceMeters
			stop.CumulativeDurationSecs = route.Stops[i-1].CumulativeDurationSecs + result.DurationSecs
		}

		totalDropoff += result.DistanceMeters
		totalRouteDuration += result.DurationSecs
		prevCoords = stop.Participant.GetCoords()
	}

	route.TotalDropoffDistanceMeters = totalDropoff

	// Calculate distance and duration to driver home (or back to activity location for institute vehicle)
	lastStop := route.Stops[len(route.Stops)-1]
	var durationToHome float64
	if route.UsedInstituteVehicle {
		result, err := h.DistanceCalc.GetDistance(ctx, lastStop.Participant.GetCoords(), activityLocation.GetCoords())
		if err == nil {
			route.DistanceToDriverHomeMeters = result.DistanceMeters
			durationToHome = result.DurationSecs
		}
	} else {
		result, err := h.DistanceCalc.GetDistance(ctx, lastStop.Participant.GetCoords(), route.Driver.GetCoords())
		if err == nil {
			route.DistanceToDriverHomeMeters = result.DistanceMeters
			durationToHome = result.DurationSecs
		}
	}

	route.TotalDistanceMeters = totalDropoff + route.DistanceToDriverHomeMeters
	route.RouteDurationSecs = totalRouteDuration + durationToHome

	// Recalculate baseline and detour for non-institute vehicle routes
	if !route.UsedInstituteVehicle && route.Driver != nil {
		baselineResult, err := h.DistanceCalc.GetDistance(ctx, activityLocation.GetCoords(), route.Driver.GetCoords())
		if err == nil {
			route.BaselineDurationSecs = baselineResult.DurationSecs
			route.DetourSecs = route.RouteDurationSecs - route.BaselineDurationSecs
		}
	} else {
		// Institute vehicle has no detour concept
		route.BaselineDurationSecs = 0
		route.DetourSecs = 0
	}
}

// calculateSummary calculates the summary for a set of routes
func (h *Handler) calculateSummary(routes []models.CalculatedRoute) models.RoutingSummary {
	var summary models.RoutingSummary

	for _, route := range routes {
		summary.TotalParticipants += len(route.Stops)
		if len(route.Stops) > 0 {
			summary.TotalDriversUsed++
		}
		summary.TotalDropoffDistanceMeters += route.TotalDropoffDistanceMeters
		summary.TotalDistanceMeters += route.TotalDistanceMeters
		if route.UsedInstituteVehicle {
			summary.UsedInstituteVehicle = true
		}
		if route.DetourSecs > summary.MaxDetourSecs {
			summary.MaxDetourSecs = route.DetourSecs
		}
		summary.SumDetourSecs += route.DetourSecs
	}

	if summary.TotalDriversUsed > 0 {
		summary.AverageDetourSecs = summary.SumDetourSecs / float64(summary.TotalDriversUsed)
	}

	return summary
}

// getUnusedDrivers returns drivers that were selected but have no assigned passengers
func getUnusedDrivers(session *RouteSession) []models.Driver {
	usedDriverIDs := make(map[int64]bool)
	for _, route := range session.CurrentRoutes {
		usedDriverIDs[route.Driver.ID] = true
	}

	var unused []models.Driver
	for _, driver := range session.SelectedDrivers {
		if !usedDriverIDs[driver.ID] {
			unused = append(unused, driver)
		}
	}
	return unused
}

// HandleGetRouteSession handles GET /api/v1/routes/edit/{sessionID}
func (h *Handler) HandleGetRouteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	session := h.RouteSession.Get(sessionID)
	if session == nil {
		h.handleNotFoundHTMX(w, r, "Session not found")
		return
	}

	summary := h.calculateSummary(session.CurrentRoutes)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           session.CurrentRoutes,
			"Summary":          summary,
			"UseMiles":         session.UseMiles,
			"ActivityLocation": session.ActivityLocation,
			"SessionID":        session.ID,
			"IsEditing":        true,
			"UnusedDrivers":    getUnusedDrivers(session),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}

// HandleAddDriver handles POST /api/v1/routes/edit/add-driver
func (h *Handler) HandleAddDriver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		DriverID  int64  `json:"driver_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, "Invalid request body")
		return
	}

	session := h.RouteSession.Get(req.SessionID)
	if session == nil {
		h.handleNotFoundHTMX(w, r, "Session not found")
		return
	}

	// Lock session for thread-safe modification
	session.mu.Lock()
	defer session.mu.Unlock()

	// Check if driver is in selected drivers
	var driverToAdd *models.Driver
	for i := range session.SelectedDrivers {
		if session.SelectedDrivers[i].ID == req.DriverID {
			driverToAdd = &session.SelectedDrivers[i]
			break
		}
	}

	if driverToAdd == nil {
		h.handleValidationErrorHTMX(w, r, "Driver not found in selected drivers")
		return
	}

	// Check if driver is already in routes
	for _, route := range session.CurrentRoutes {
		if route.Driver.ID == req.DriverID {
			h.handleValidationErrorHTMX(w, r, "Driver is already in routes")
			return
		}
	}

	// Create empty route for this driver
	newRoute := models.CalculatedRoute{
		Driver: driverToAdd,
		Stops:  []models.RouteStop{},
	}

	session.CurrentRoutes = append(session.CurrentRoutes, newRoute)

	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Added unused driver %d (%s) to routes", req.DriverID, driverToAdd.Name)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           session.CurrentRoutes,
			"Summary":          summary,
			"UseMiles":         session.UseMiles,
			"ActivityLocation": session.ActivityLocation,
			"SessionID":        session.ID,
			"IsEditing":        true,
			"UnusedDrivers":    getUnusedDrivers(session),
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}
