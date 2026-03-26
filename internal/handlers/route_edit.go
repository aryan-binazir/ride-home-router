package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
)

const (
	routeSessionTTL             = 2 * time.Hour
	routeSessionCleanupInterval = 15 * time.Minute
)

// RouteSession stores calculated routes for editing
type RouteSession struct {
	ID                string
	OriginalRoutes    []models.CalculatedRoute
	CurrentRoutes     []models.CalculatedRoute
	SelectedDrivers   []models.Driver
	DriverOrgVehicles map[int64]*models.OrganizationVehicle
	ActivityLocation  *models.ActivityLocation
	UseMiles          bool
	RouteTime         string
	Mode              string // "pickup" or "dropoff"
	LastAccessedAt    time.Time
	mu                sync.Mutex // Protects session data during modifications
}

// RouteSessionStore manages route editing sessions in memory
type RouteSessionStore struct {
	sessions        map[string]*RouteSession
	mu              sync.RWMutex
	ttl             time.Duration
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
	cleanupDone     chan struct{}
	closeOnce       sync.Once
}

// NewRouteSessionStore creates a new session store
func NewRouteSessionStore() *RouteSessionStore {
	return newRouteSessionStore(routeSessionTTL, routeSessionCleanupInterval)
}

func newRouteSessionStore(ttl, cleanupInterval time.Duration) *RouteSessionStore {
	store := &RouteSessionStore{
		sessions:        make(map[string]*RouteSession),
		ttl:             ttl,
		cleanupInterval: cleanupInterval,
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
	}

	go store.cleanupLoop()
	return store
}

func (s *RouteSessionStore) Create(routes []models.CalculatedRoute, drivers []models.Driver, activityLocation *models.ActivityLocation, useMiles bool, routeTime, mode string, driverOrgVehicles map[int64]*models.OrganizationVehicle) *RouteSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := generateSessionID()

	// Deep copy routes for original
	originalRoutes := deepCopyRoutes(routes)
	currentRoutes := deepCopyRoutes(routes)

	session := &RouteSession{
		ID:                id,
		OriginalRoutes:    originalRoutes,
		CurrentRoutes:     currentRoutes,
		SelectedDrivers:   drivers,
		DriverOrgVehicles: copyOrgVehicleAssignments(driverOrgVehicles),
		ActivityLocation:  activityLocation,
		UseMiles:          useMiles,
		RouteTime:         routeTime,
		Mode:              mode,
		LastAccessedAt:    time.Now(),
	}

	s.sessions[id] = session
	log.Printf("[SESSION] Created route session: id=%s routes=%d drivers=%d mode=%s", id, len(routes), len(drivers), mode)
	return session
}

func (s *RouteSessionStore) Get(id string) *RouteSession {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.sessions[id]
	if session == nil {
		return nil
	}
	if s.sessionExpired(session, now) {
		delete(s.sessions, id)
		log.Printf("[SESSION] Expired route session: id=%s", id)
		return nil
	}

	session.LastAccessedAt = now
	return session
}

// Update executes a function on a session while holding the write lock.
// This ensures thread-safe modifications to session data.
// Returns false if the session doesn't exist.
func (s *RouteSessionStore) Update(id string, fn func(*RouteSession)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	session := s.sessions[id]
	if session == nil {
		return false
	}
	if s.sessionExpired(session, now) {
		delete(s.sessions, id)
		log.Printf("[SESSION] Expired route session during update: id=%s", id)
		return false
	}
	session.LastAccessedAt = now
	fn(session)
	return true
}

func (s *RouteSessionStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	log.Printf("[SESSION] Deleted route session: id=%s", id)
}

func (s *RouteSessionStore) Close() {
	s.closeOnce.Do(func() {
		close(s.stopCleanup)
		<-s.cleanupDone
	})
}

func (s *RouteSessionStore) cleanupLoop() {
	defer close(s.cleanupDone)

	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.deleteExpired(time.Now())
		case <-s.stopCleanup:
			return
		}
	}
}

func (s *RouteSessionStore) deleteExpired(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, session := range s.sessions {
		if s.sessionExpired(session, now) {
			delete(s.sessions, id)
			log.Printf("[SESSION] Expired route session: id=%s", id)
		}
	}
}

func (s *RouteSessionStore) sessionExpired(session *RouteSession, now time.Time) bool {
	return now.Sub(session.LastAccessedAt) > s.ttl
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
			OrgVehicleID:               route.OrgVehicleID,
			OrgVehicleName:             route.OrgVehicleName,
			EffectiveCapacity:          route.EffectiveCapacity,
			BaselineDurationSecs:       route.BaselineDurationSecs,
			RouteDurationSecs:          route.RouteDurationSecs,
			DetourSecs:                 route.DetourSecs,
			Mode:                       route.Mode,
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

func copyOrgVehicleAssignments(assignments map[int64]*models.OrganizationVehicle) map[int64]*models.OrganizationVehicle {
	if len(assignments) == 0 {
		return map[int64]*models.OrganizationVehicle{}
	}

	result := make(map[int64]*models.OrganizationVehicle, len(assignments))
	for driverID, vehicle := range assignments {
		if vehicle == nil {
			continue
		}
		copy := *vehicle
		result[driverID] = &copy
	}
	return result
}

func buildRoutingPayload(routes []models.CalculatedRoute, summary models.RoutingSummary, mode string) models.RoutingResult {
	return models.RoutingResult{
		Routes:   routes,
		Summary:  summary,
		Warnings: []string{},
		Mode:     mode,
	}
}

func buildRouteResultsView(routes []models.CalculatedRoute, summary models.RoutingSummary, activityLocation *models.ActivityLocation, useMiles bool, routeTime, sessionID string, isEditing bool, unusedDrivers []models.Driver, mode string) map[string]interface{} {
	return map[string]interface{}{
		"Routes":           routes,
		"Summary":          summary,
		"UseMiles":         useMiles,
		"ActivityLocation": activityLocation,
		"RouteTime":        routeTime,
		"SessionID":        sessionID,
		"IsEditing":        isEditing,
		"UnusedDrivers":    unusedDrivers,
		"Mode":             mode,
		"RoutingPayload":   buildRoutingPayload(routes, summary, mode),
	}
}

func (h *Handler) recalculateRoute(ctx context.Context, activityLocation *models.ActivityLocation, mode string, route *models.CalculatedRoute) error {
	if activityLocation == nil {
		return fmt.Errorf("activity location is required")
	}
	return routing.PopulateRouteMetrics(ctx, h.DistanceCalc, activityLocation.GetCoords(), routing.RouteMode(mode), route)
}

// HandleMoveParticipant handles POST /api/v1/routes/edit/move-participant
func (h *Handler) HandleMoveParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID        string `json:"session_id"`
		ParticipantID    int64  `json:"participant_id"`
		FromRouteIndex   int    `json:"from_route_index"`
		ToRouteIndex     int    `json:"to_route_index"`
		InsertAtPosition int    `json:"insert_at_position"` // -1 for end
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

	backupRoutes := deepCopyRoutes(session.CurrentRoutes)

	if req.FromRouteIndex < 0 || req.FromRouteIndex >= len(session.CurrentRoutes) ||
		req.ToRouteIndex < 0 || req.ToRouteIndex >= len(session.CurrentRoutes) {
		h.handleValidationErrorHTMX(w, r, "Invalid route index")
		return
	}

	fromRoute := &session.CurrentRoutes[req.FromRouteIndex]
	toRoute := &session.CurrentRoutes[req.ToRouteIndex]

	// Check capacity (use effective capacity which may be org vehicle capacity)
	effectiveCapacity := toRoute.EffectiveCapacity
	if effectiveCapacity == 0 {
		effectiveCapacity = toRoute.Driver.VehicleCapacity
	}
	if len(toRoute.Stops) >= effectiveCapacity {
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
	if err := h.recalculateRoute(r.Context(), session.ActivityLocation, session.Mode, fromRoute); err != nil {
		session.CurrentRoutes = backupRoutes
		h.handleInternalError(w, err)
		return
	}
	if err := h.recalculateRoute(r.Context(), session.ActivityLocation, session.Mode, toRoute); err != nil {
		session.CurrentRoutes = backupRoutes
		h.handleInternalError(w, err)
		return
	}

	// Recalculate summary
	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Moved participant %d from route %d to route %d",
		req.ParticipantID, req.FromRouteIndex, req.ToRouteIndex)

	// Return updated routes
	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", buildRouteResultsView(session.CurrentRoutes, summary, session.ActivityLocation, session.UseMiles, session.RouteTime, session.ID, true, getUnusedDrivers(session), session.Mode))
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

	backupRoutes := deepCopyRoutes(session.CurrentRoutes)

	if req.RouteIndex1 < 0 || req.RouteIndex1 >= len(session.CurrentRoutes) ||
		req.RouteIndex2 < 0 || req.RouteIndex2 >= len(session.CurrentRoutes) {
		h.handleValidationErrorHTMX(w, r, "Invalid route index")
		return
	}

	route1 := &session.CurrentRoutes[req.RouteIndex1]
	route2 := &session.CurrentRoutes[req.RouteIndex2]

	// Check capacity constraints (use effective capacity which may be org vehicle capacity)
	cap1 := route1.EffectiveCapacity
	if cap1 == 0 {
		if route1.Driver == nil {
			h.handleValidationErrorHTMX(w, r, "Cannot swap - route is missing a driver")
			return
		}
		cap1 = route1.Driver.VehicleCapacity
	}
	cap2 := route2.EffectiveCapacity
	if cap2 == 0 {
		if route2.Driver == nil {
			h.handleValidationErrorHTMX(w, r, "Cannot swap - route is missing a driver")
			return
		}
		cap2 = route2.Driver.VehicleCapacity
	}
	if len(route1.Stops) > cap2 || len(route2.Stops) > cap1 {
		h.handleValidationErrorHTMX(w, r, "Cannot swap - capacity constraints violated")
		return
	}

	// Swap drivers
	route1.Driver, route2.Driver = route2.Driver, route1.Driver

	// Recalculate distances for both routes (driver home changed)
	if err := h.recalculateRoute(r.Context(), session.ActivityLocation, session.Mode, route1); err != nil {
		session.CurrentRoutes = backupRoutes
		h.handleInternalError(w, err)
		return
	}
	if err := h.recalculateRoute(r.Context(), session.ActivityLocation, session.Mode, route2); err != nil {
		session.CurrentRoutes = backupRoutes
		h.handleInternalError(w, err)
		return
	}

	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Swapped drivers between routes %d and %d", req.RouteIndex1, req.RouteIndex2)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", buildRouteResultsView(session.CurrentRoutes, summary, session.ActivityLocation, session.UseMiles, session.RouteTime, session.ID, true, getUnusedDrivers(session), session.Mode))
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
		h.renderTemplate(w, "route_results", buildRouteResultsView(session.CurrentRoutes, summary, session.ActivityLocation, session.UseMiles, session.RouteTime, session.ID, true, getUnusedDrivers(session), session.Mode))
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
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
		if route.DetourSecs > summary.MaxDetourSecs {
			summary.MaxDetourSecs = route.DetourSecs
		}
		summary.SumDetourSecs += route.DetourSecs
	}
	summary.OrgVehiclesUsed = countUsedOrgVehicles(routes)

	if summary.TotalDriversUsed > 0 {
		summary.AverageDetourSecs = summary.SumDetourSecs / float64(summary.TotalDriversUsed)
	}

	return summary
}

// getUnusedDrivers returns drivers that were selected but have no assigned passengers
func getUnusedDrivers(session *RouteSession) []models.Driver {
	usedDriverIDs := make(map[int64]bool)
	for _, route := range session.CurrentRoutes {
		if route.Driver == nil {
			continue
		}
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
		if route.Driver == nil {
			continue
		}
		if route.Driver.ID == req.DriverID {
			h.handleValidationErrorHTMX(w, r, "Driver is already in routes")
			return
		}
	}

	// Create empty route for this driver
	newRoute := models.CalculatedRoute{
		Driver:            driverToAdd,
		Stops:             []models.RouteStop{},
		EffectiveCapacity: driverToAdd.VehicleCapacity,
		Mode:              session.Mode,
	}
	if van, ok := session.DriverOrgVehicles[driverToAdd.ID]; ok && van != nil {
		newRoute.OrgVehicleID = van.ID
		newRoute.OrgVehicleName = van.Name
		newRoute.EffectiveCapacity = van.Capacity
	}
	if err := h.recalculateRoute(r.Context(), session.ActivityLocation, session.Mode, &newRoute); err != nil {
		h.handleInternalError(w, err)
		return
	}

	session.CurrentRoutes = append(session.CurrentRoutes, newRoute)

	summary := h.calculateSummary(session.CurrentRoutes)

	log.Printf("[EDIT] Added unused driver %d (%s) to routes", req.DriverID, driverToAdd.Name)

	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", buildRouteResultsView(session.CurrentRoutes, summary, session.ActivityLocation, session.UseMiles, session.RouteTime, session.ID, true, getUnusedDrivers(session), session.Mode))
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     session.CurrentRoutes,
		"summary":    summary,
		"session_id": session.ID,
	})
}
