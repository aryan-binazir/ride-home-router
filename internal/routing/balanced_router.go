package routing

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// BalancedRouter implements fair distribution routing that:
// 1. Uses all available drivers
// 2. Minimizes the maximum route duration (load balancing)
type BalancedRouter struct {
	distanceCalc    distance.DistanceCalculator
	instituteCoords models.Coordinates
	mode            RouteMode
}

// NewBalancedRouter creates a router that balances load across all drivers
func NewBalancedRouter(distanceCalc distance.DistanceCalculator) Router {
	return &BalancedRouter{
		distanceCalc: distanceCalc,
	}
}

// getOrigin returns the starting point for a route based on the mode
func (r *BalancedRouter) getOrigin(driver *models.Driver) models.Coordinates {
	if r.mode == RouteModePickup {
		return driver.GetCoords()
	}
	return r.instituteCoords // dropoff: start from institute
}

// getDestination returns the ending point for a route based on the mode
func (r *BalancedRouter) getDestination(driver *models.Driver) models.Coordinates {
	if r.mode == RouteModePickup {
		return r.instituteCoords
	}
	return driver.GetCoords() // dropoff: end at driver home
}

func (r *BalancedRouter) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	totalStart := time.Now()

	// Initialize mode and institute coords for this calculation
	r.instituteCoords = req.InstituteCoords
	r.mode = req.Mode
	if r.mode == "" {
		r.mode = RouteModeDropoff
	}

	log.Printf("[BALANCED] Starting calculation: participants=%d drivers=%d mode=%s",
		len(req.Participants), len(req.Drivers), r.mode)

	// Handle empty participants
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{},
			Warnings: []string{},
			Mode:     string(r.mode),
		}, nil
	}

	// Handle empty drivers
	if len(req.Drivers) == 0 {
		return nil, &ErrRoutingFailed{
			Reason:            "No drivers available",
			UnassignedCount:   len(req.Participants),
			TotalCapacity:     0,
			TotalParticipants: len(req.Participants),
		}
	}

	// Prewarm distance cache
	prewarmStart := time.Now()
	allPoints := []models.Coordinates{req.InstituteCoords}
	for _, p := range req.Participants {
		allPoints = append(allPoints, p.GetCoords())
	}
	for _, d := range req.Drivers {
		allPoints = append(allPoints, d.GetCoords())
	}
	if err := r.distanceCalc.PrewarmCache(ctx, allPoints); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Prewarm cache: %v", time.Since(prewarmStart))

	// Initialize routes for each driver
	routes := make(map[int64]*balancedRoute)
	driverIDs := make([]int64, 0, len(req.Drivers))
	for i := range req.Drivers {
		driver := &req.Drivers[i]
		routes[driver.ID] = &balancedRoute{
			driver:        driver,
			stops:         []*models.Participant{},
			totalDuration: 0,
		}
		driverIDs = append(driverIDs, driver.ID)
	}

	// Build unassigned list
	unassigned := make([]*models.Participant, len(req.Participants))
	for i := range req.Participants {
		unassigned[i] = &req.Participants[i]
	}

	// Phase 1: Round-robin insertion to distribute evenly across drivers
	phase1Start := time.Now()
	unassigned, err := r.roundRobinInsertion(ctx, routes, driverIDs, unassigned)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 1 (round-robin): %v", time.Since(phase1Start))

	// Phase 2: 2-opt on each route
	phase2Start := time.Now()
	if err := r.optimizeAllRoutes(ctx, routes); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 2 (2-opt): %v", time.Since(phase2Start))

	// Phase 3: Min-max inter-route optimization
	phase3Start := time.Now()
	iterations := r.minMaxOptimize(ctx, routes, driverIDs)
	log.Printf("[TIMING] Phase 3 (min-max): %v (iterations=%d)", time.Since(phase3Start), iterations)

	// Check for unassigned participants
	if len(unassigned) > 0 {
		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	// Build result
	result, err := r.buildResult(ctx, routes, len(req.Participants))
	if err != nil {
		return nil, err
	}

	log.Printf("[BALANCED] Complete: drivers_used=%d total_distance=%.0fm",
		result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)
	log.Printf("[TIMING] TOTAL: %v", time.Since(totalStart))

	return result, nil
}

// balancedRoute tracks route state including running duration
type balancedRoute struct {
	driver        *models.Driver
	stops         []*models.Participant
	totalDuration float64
}

// roundRobinInsertion assigns participants by cycling through drivers
// Groups participants from the same household and assigns them together
func (r *BalancedRouter) roundRobinInsertion(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) ([]*models.Participant, error) {
	// Sort drivers by ID for consistent ordering
	sort.Slice(driverIDs, func(i, j int) bool {
		return driverIDs[i] < driverIDs[j]
	})

	// Group participants by address
	groups := groupParticipantsByAddress(unassigned)

	totalParticipants := len(unassigned)
	log.Printf("[BALANCED] Distributing %d participants (%d household groups) across %d drivers",
		totalParticipants, len(groups), len(driverIDs))

	// Round-robin through drivers, assigning best-fit group to each
	driverIndex := 0
	maxRounds := len(groups) * len(driverIDs) * 2 // Safety limit

	for len(groups) > 0 && maxRounds > 0 {
		maxRounds--

		// Find next driver with capacity for at least 1 participant
		foundDriver := false
		startIndex := driverIndex
		for {
			driverID := driverIDs[driverIndex]
			route := routes[driverID]

			if len(route.stops) < route.driver.VehicleCapacity {
				foundDriver = true
				break
			}

			driverIndex = (driverIndex + 1) % len(driverIDs)
			if driverIndex == startIndex {
				break // All drivers full
			}
		}

		if !foundDriver {
			break // All drivers at capacity
		}

		currentDriverID := driverIDs[driverIndex]
		route := routes[currentDriverID]
		remainingCapacity := route.driver.VehicleCapacity - len(route.stops)

		// Find best group and position for this driver (minimize insertion cost)
		bestCost := math.Inf(1)
		var bestGroup *participantGroup
		var bestGroupIndex int
		var bestPosition int

		for groupIdx, group := range groups {
			groupSize := len(group.members)

			// Check if group fits in remaining capacity
			if groupSize > remainingCapacity {
				// Group too large - skip for now
				// In a more sophisticated implementation, we could split the group
				continue
			}

			// Try all insertion positions for this group
			for pos := 0; pos <= len(route.stops); pos++ {
				cost, err := r.groupInsertionCost(ctx, route, group, pos)
				if err != nil {
					return nil, err
				}

				if cost < bestCost {
					bestCost = cost
					bestGroup = group
					bestGroupIndex = groupIdx
					bestPosition = pos
				}
			}
		}

		// If no group fits, try to assign single participants from groups
		if bestGroup == nil {
			// Find smallest participant we can fit
			for groupIdx, group := range groups {
				if len(group.members) == 0 {
					continue
				}

				// Try just the first member of the group
				for pos := 0; pos <= len(route.stops); pos++ {
					cost, err := r.insertionCost(ctx, route, group.members[0], pos)
					if err != nil {
						return nil, err
					}

					if cost < bestCost {
						bestCost = cost
						// Create a temporary single-person group
						bestGroup = &participantGroup{
							members: []*models.Participant{group.members[0]},
							address: group.address,
							lat:     group.lat,
							lng:     group.lng,
						}
						bestGroupIndex = groupIdx
						bestPosition = pos
					}
				}
			}
		}

		if bestGroup == nil {
			break // No group can be assigned
		}

		// Insert the group
		route.stops = insertGroupAt(route.stops, bestGroup, bestPosition)
		r.updateRouteDuration(ctx, route)

		memberNames := make([]string, len(bestGroup.members))
		for i, m := range bestGroup.members {
			memberNames[i] = m.Name
		}

		if len(bestGroup.members) == 1 {
			log.Printf("[BALANCED] Assigned %s to %s (pos=%d, cost=%.0fs)",
				memberNames[0], route.driver.Name, bestPosition, bestCost)
		} else {
			log.Printf("[BALANCED] Assigned household group [%v] to %s (pos=%d, cost=%.0fs, size=%d)",
				memberNames, route.driver.Name, bestPosition, bestCost, len(bestGroup.members))
		}

		// Remove assigned participants from the group
		originalGroup := groups[bestGroupIndex]
		if len(bestGroup.members) == len(originalGroup.members) {
			// Entire group was assigned - remove from groups list
			groups = append(groups[:bestGroupIndex], groups[bestGroupIndex+1:]...)
		} else {
			// Partial assignment - remove assigned member from original group
			// This handles the case where we split a group
			assignedID := bestGroup.members[0].ID
			newMembers := make([]*models.Participant, 0, len(originalGroup.members)-1)
			for _, m := range originalGroup.members {
				if m.ID != assignedID {
					newMembers = append(newMembers, m)
				}
			}
			originalGroup.members = newMembers
		}

		// Move to next driver (round-robin)
		driverIndex = (driverIndex + 1) % len(driverIDs)
	}

	// Build list of unassigned participants from remaining groups
	unassignedResult := make([]*models.Participant, 0)
	for _, group := range groups {
		unassignedResult = append(unassignedResult, group.members...)
	}

	return unassignedResult, nil
}

// insertionCost calculates the additional duration to insert p at position pos
func (r *BalancedRouter) insertionCost(ctx context.Context, route *balancedRoute, p *models.Participant, pos int) (float64, error) {
	var prev, next models.Coordinates

	if pos == 0 {
		prev = r.getOrigin(route.driver)
	} else {
		prev = route.stops[pos-1].GetCoords()
	}

	if pos < len(route.stops) {
		next = route.stops[pos].GetCoords()
	} else {
		// Inserting at end - just duration from prev to new stop
		distPrevP, err := r.distanceCalc.GetDistance(ctx, prev, p.GetCoords())
		if err != nil {
			return 0, err
		}
		return distPrevP.DurationSecs, nil
	}

	// Cost = duration(prev, p) + duration(p, next) - duration(prev, next)
	distPrevP, err := r.distanceCalc.GetDistance(ctx, prev, p.GetCoords())
	if err != nil {
		return 0, err
	}

	distPNext, err := r.distanceCalc.GetDistance(ctx, p.GetCoords(), next)
	if err != nil {
		return 0, err
	}

	distPrevNext, err := r.distanceCalc.GetDistance(ctx, prev, next)
	if err != nil {
		return 0, err
	}

	return distPrevP.DurationSecs + distPNext.DurationSecs - distPrevNext.DurationSecs, nil
}

// optimizeAllRoutes runs 2-opt on each route and updates durations
func (r *BalancedRouter) optimizeAllRoutes(ctx context.Context, routes map[int64]*balancedRoute) error {
	for _, route := range routes {
		if len(route.stops) >= 3 {
			optimized, err := r.twoOpt(ctx, route, route.stops)
			if err != nil {
				return err
			}
			route.stops = optimized
		}
		r.updateRouteDuration(ctx, route)
	}
	return nil
}

// twoOpt applies 2-opt optimization to reduce route duration
func (r *BalancedRouter) twoOpt(ctx context.Context, route *balancedRoute, stops []*models.Participant) ([]*models.Participant, error) {
	if len(stops) < 3 {
		return stops, nil
	}

	start := r.getOrigin(route.driver)
	improved := true
	for improved {
		improved = false
		for i := 0; i < len(stops)-1; i++ {
			for j := i + 2; j <= len(stops); j++ {
				var beforeI models.Coordinates
				if i == 0 {
					beforeI = start
				} else {
					beforeI = stops[i-1].GetCoords()
				}

				dist1, err := r.distanceCalc.GetDistance(ctx, beforeI, stops[i].GetCoords())
				if err != nil {
					return nil, err
				}

				var afterJ models.Coordinates
				if j < len(stops) {
					afterJ = stops[j].GetCoords()
				} else {
					dist1New, err := r.distanceCalc.GetDistance(ctx, beforeI, stops[j-1].GetCoords())
					if err != nil {
						return nil, err
					}
					if dist1New.DurationSecs < dist1.DurationSecs {
						reverse(stops, i, j-1)
						improved = true
					}
					continue
				}

				dist2, err := r.distanceCalc.GetDistance(ctx, stops[j-1].GetCoords(), afterJ)
				if err != nil {
					return nil, err
				}

				currentDuration := dist1.DurationSecs + dist2.DurationSecs

				dist1New, err := r.distanceCalc.GetDistance(ctx, beforeI, stops[j-1].GetCoords())
				if err != nil {
					return nil, err
				}

				dist2New, err := r.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), afterJ)
				if err != nil {
					return nil, err
				}

				newDuration := dist1New.DurationSecs + dist2New.DurationSecs

				if newDuration < currentDuration {
					reverse(stops, i, j-1)
					improved = true
				}
			}
		}
	}

	return stops, nil
}

// minMaxOptimize moves participants between routes to minimize the maximum route duration
func (r *BalancedRouter) minMaxOptimize(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64) int {
	maxIterations := 50
	iteration := 0

	// Calculate minimum stops per driver (hard floor to ensure all drivers used)
	totalParticipants := 0
	for _, route := range routes {
		totalParticipants += len(route.stops)
	}
	minStopsPerDriver := 1 // At minimum, every driver should have 1 stop
	if totalParticipants < len(driverIDs) {
		minStopsPerDriver = 0 // Not enough participants for all drivers
	}

	for iteration < maxIterations {
		iteration++

		// Find routes sorted by duration (longest first)
		type routeDuration struct {
			id       int64
			duration float64
		}
		routesByDuration := make([]routeDuration, 0, len(driverIDs))
		for _, id := range driverIDs {
			route := routes[id]
			duration := r.calculateRouteDuration(ctx, route)
			routesByDuration = append(routesByDuration, routeDuration{id, duration})
		}
		sort.Slice(routesByDuration, func(i, j int) bool {
			return routesByDuration[i].duration > routesByDuration[j].duration
		})

		currentMaxDuration := routesByDuration[0].duration
		if currentMaxDuration == 0 {
			break
		}

		// Try to reduce routes starting from longest
		foundMove := false
		for _, rd := range routesByDuration {
			srcRoute := routes[rd.id]

			// Skip if at minimum stops
			if len(srcRoute.stops) <= minStopsPerDriver {
				continue
			}

			// Try moving each participant to a shorter route
			var bestMove struct {
				destID          int64
				srcPos, destPos int
				participant     *models.Participant
				newMaxDuration  float64
			}
			bestMove.newMaxDuration = currentMaxDuration

			for srcPos := 0; srcPos < len(srcRoute.stops); srcPos++ {
				p := srcRoute.stops[srcPos]

				for _, destID := range driverIDs {
					if destID == rd.id {
						continue
					}

					destRoute := routes[destID]
					if len(destRoute.stops) >= destRoute.driver.VehicleCapacity {
						continue
					}

					for destPos := 0; destPos <= len(destRoute.stops); destPos++ {
						// Simulate the move
						newSrcStops := removeAt(srcRoute.stops, srcPos)
						newDestStops := insertAt(destRoute.stops, p, destPos)

						oldSrcStops := srcRoute.stops
						oldDestStops := destRoute.stops
						srcRoute.stops = newSrcStops
						destRoute.stops = newDestStops

						// Calculate new max duration
						newMaxDuration := 0.0
						for _, id := range driverIDs {
							duration := r.calculateRouteDuration(ctx, routes[id])
							if duration > newMaxDuration {
								newMaxDuration = duration
							}
						}

						// Restore
						srcRoute.stops = oldSrcStops
						destRoute.stops = oldDestStops

						// Accept if it reduces the maximum
						if newMaxDuration < bestMove.newMaxDuration-10 { // 10 second threshold
							bestMove.destID = destID
							bestMove.srcPos = srcPos
							bestMove.destPos = destPos
							bestMove.participant = p
							bestMove.newMaxDuration = newMaxDuration
						}
					}
				}
			}

			// Execute best move if found
			if bestMove.participant != nil {
				destRoute := routes[bestMove.destID]

				srcRoute.stops = removeAt(srcRoute.stops, bestMove.srcPos)
				destRoute.stops = insertAt(destRoute.stops, bestMove.participant, bestMove.destPos)

				// Re-optimize affected routes with 2-opt
				r.twoOpt(ctx, srcRoute, srcRoute.stops)
				r.twoOpt(ctx, destRoute, destRoute.stops)

				r.updateRouteDuration(ctx, srcRoute)
				r.updateRouteDuration(ctx, destRoute)

				log.Printf("[BALANCED] Moved %s from %s to %s (max: %.0fs -> %.0fs)",
					bestMove.participant.Name, srcRoute.driver.Name, destRoute.driver.Name,
					currentMaxDuration, bestMove.newMaxDuration)

				foundMove = true
				break // Restart from longest route
			}
		}

		if !foundMove {
			break // No improving moves found
		}
	}

	return iteration
}

// calculateRouteDuration computes duration from origin through all stops
func (r *BalancedRouter) calculateRouteDuration(ctx context.Context, route *balancedRoute) float64 {
	if len(route.stops) == 0 {
		return 0
	}

	total := 0.0
	prev := r.getOrigin(route.driver)
	for _, stop := range route.stops {
		dist, err := r.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err == nil {
			total += dist.DurationSecs
		}
		prev = stop.GetCoords()
	}
	return total
}

// updateRouteDuration recalculates and caches the total duration for a route
func (r *BalancedRouter) updateRouteDuration(ctx context.Context, route *balancedRoute) {
	if len(route.stops) == 0 {
		route.totalDuration = 0
		return
	}

	total := 0.0
	prev := r.getOrigin(route.driver)
	for _, stop := range route.stops {
		dist, err := r.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err == nil {
			total += dist.DurationSecs
		}
		prev = stop.GetCoords()
	}
	route.totalDuration = total
}

// buildResult creates the final routing result
func (r *BalancedRouter) buildResult(ctx context.Context, routes map[int64]*balancedRoute, totalParticipants int) (*models.RoutingResult, error) {
	calculatedRoutes := make([]models.CalculatedRoute, 0)
	totalDropoff := 0.0
	totalDist := 0.0
	driversUsed := 0

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue
		}

		driversUsed++

		routeStops := make([]models.RouteStop, len(route.stops))
		cumulativeDistance := 0.0
		cumulativeDuration := 0.0

		origin := r.getOrigin(route.driver)
		prev := origin
		for i, p := range route.stops {
			dist, err := r.distanceCalc.GetDistance(ctx, prev, p.GetCoords())
			if err != nil {
				return nil, err
			}

			cumulativeDistance += dist.DistanceMeters
			cumulativeDuration += dist.DurationSecs

			routeStops[i] = models.RouteStop{
				Order:                    i,
				Participant:              p,
				DistanceFromPrevMeters:   dist.DistanceMeters,
				CumulativeDistanceMeters: cumulativeDistance,
				DurationFromPrevSecs:     dist.DurationSecs,
				CumulativeDurationSecs:   cumulativeDuration,
			}

			prev = p.GetCoords()
		}

		destination := r.getDestination(route.driver)
		distToEnd, err := r.distanceCalc.GetDistance(ctx, prev, destination)
		if err != nil {
			return nil, err
		}

		baseline, err := r.distanceCalc.GetDistance(ctx, origin, destination)
		if err != nil {
			return nil, err
		}

		totalDropoff += cumulativeDistance
		totalDist += cumulativeDistance + distToEnd.DistanceMeters

		calculatedRoutes = append(calculatedRoutes, models.CalculatedRoute{
			Driver:                     route.driver,
			Stops:                      routeStops,
			TotalDropoffDistanceMeters: cumulativeDistance,
			DistanceToDriverHomeMeters: distToEnd.DistanceMeters,
			TotalDistanceMeters:        cumulativeDistance + distToEnd.DistanceMeters,
			EffectiveCapacity:          route.driver.VehicleCapacity,
			BaselineDurationSecs:       baseline.DurationSecs,
			RouteDurationSecs:          cumulativeDuration + distToEnd.DurationSecs,
			DetourSecs:                 (cumulativeDuration + distToEnd.DurationSecs) - baseline.DurationSecs,
			Mode:                       string(r.mode),
		})
	}

	return &models.RoutingResult{
		Routes: calculatedRoutes,
		Summary: models.RoutingSummary{
			TotalParticipants:          totalParticipants,
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoff,
			TotalDistanceMeters:        totalDist,
			UnassignedParticipants:     []int64{},
		},
		Warnings: []string{},
		Mode:     string(r.mode),
	}, nil
}

// participantGroup represents participants from the same household
type participantGroup struct {
	members []*models.Participant
	address string
	lat     float64
	lng     float64
}

// groupParticipantsByAddress groups participants by their address coordinates
// Participants with the same rounded lat/lng are considered to be from the same household
func groupParticipantsByAddress(participants []*models.Participant) []*participantGroup {
	// Map address coordinates to group
	addressMap := make(map[string]*participantGroup)

	for _, p := range participants {
		// Round coordinates to 5 decimal places for consistent grouping
		roundedLat := models.RoundCoordinate(p.Lat)
		roundedLng := models.RoundCoordinate(p.Lng)
		key := coordinateKey(roundedLat, roundedLng)

		if group, exists := addressMap[key]; exists {
			// Add to existing group
			group.members = append(group.members, p)
		} else {
			// Create new group
			addressMap[key] = &participantGroup{
				members: []*models.Participant{p},
				address: p.Address,
				lat:     roundedLat,
				lng:     roundedLng,
			}
		}
	}

	// Convert map to slice
	groups := make([]*participantGroup, 0, len(addressMap))
	for _, group := range addressMap {
		groups = append(groups, group)
	}

	// Sort groups by size (larger groups first) for better initial assignment
	sort.Slice(groups, func(i, j int) bool {
		return len(groups[i].members) > len(groups[j].members)
	})

	return groups
}

// coordinateKey creates a unique key for a coordinate pair
func coordinateKey(lat, lng float64) string {
	return fmt.Sprintf("%.5f,%.5f", lat, lng)
}

// insertGroupAt inserts all members of a group consecutively at the specified position
func insertGroupAt(stops []*models.Participant, group *participantGroup, pos int) []*models.Participant {
	// Create new slice with room for the group
	newStops := make([]*models.Participant, len(stops)+len(group.members))

	// Copy elements before insertion point
	copy(newStops, stops[:pos])

	// Insert group members
	copy(newStops[pos:], group.members)

	// Copy elements after insertion point
	copy(newStops[pos+len(group.members):], stops[pos:])

	return newStops
}

// groupInsertionCost calculates the cost to insert all group members at a position
func (r *BalancedRouter) groupInsertionCost(ctx context.Context, route *balancedRoute, group *participantGroup, pos int) (float64, error) {
	if len(group.members) == 0 {
		return 0, nil
	}

	// For a group, we need to calculate the cost of inserting all members sequentially
	// We'll insert them in order and calculate the total additional duration

	var prev models.Coordinates
	if pos == 0 {
		prev = r.getOrigin(route.driver)
	} else {
		prev = route.stops[pos-1].GetCoords()
	}

	var next models.Coordinates
	hasNext := pos < len(route.stops)
	if hasNext {
		next = route.stops[pos].GetCoords()
	}

	// Calculate cost through all group members
	totalCost := 0.0
	currentPrev := prev

	for _, member := range group.members {
		dist, err := r.distanceCalc.GetDistance(ctx, currentPrev, member.GetCoords())
		if err != nil {
			return 0, err
		}
		totalCost += dist.DurationSecs
		currentPrev = member.GetCoords()
	}

	// Add cost from last group member to next stop (or end of route)
	if hasNext {
		distToNext, err := r.distanceCalc.GetDistance(ctx, currentPrev, next)
		if err != nil {
			return 0, err
		}
		totalCost += distToNext.DurationSecs

		// Subtract the original direct distance
		distPrevNext, err := r.distanceCalc.GetDistance(ctx, prev, next)
		if err != nil {
			return 0, err
		}
		totalCost -= distPrevNext.DurationSecs
	}

	return totalCost, nil
}

// Note: Helper functions (insertAt, removeAt, removeParticipant, reverse)
// are defined in distance_minimizer.go and shared across routing implementations
