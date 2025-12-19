package routing

import (
	"context"
	"log"
	"math"
	"sort"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// BalancedRouter implements fair distribution routing that:
// 1. Uses all available drivers
// 2. Minimizes the maximum route distance (load balancing)
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
			totalDistance: 0,
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

// balancedRoute tracks route state including running distance
type balancedRoute struct {
	driver        *models.Driver
	stops         []*models.Participant
	totalDistance float64
}

// roundRobinInsertion assigns participants by cycling through drivers
// Each driver gets one participant before any driver gets a second
func (r *BalancedRouter) roundRobinInsertion(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) ([]*models.Participant, error) {
	// Sort drivers by ID for consistent ordering
	sort.Slice(driverIDs, func(i, j int) bool {
		return driverIDs[i] < driverIDs[j]
	})

	log.Printf("[BALANCED] Distributing %d participants across %d drivers", len(unassigned), len(driverIDs))

	// Round-robin through drivers, assigning best-fit participant to each
	driverIndex := 0
	maxRounds := len(unassigned) + len(driverIDs) // Safety limit

	for len(unassigned) > 0 && maxRounds > 0 {
		maxRounds--

		// Find next driver with capacity
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

		// Find best participant and position for this driver (minimize insertion cost)
		bestCost := math.Inf(1)
		var bestParticipant *models.Participant
		var bestPosition int

		for _, p := range unassigned {
			for pos := 0; pos <= len(route.stops); pos++ {
				cost, err := r.insertionCost(ctx, route, p, pos)
				if err != nil {
					return nil, err
				}

				if cost < bestCost {
					bestCost = cost
					bestParticipant = p
					bestPosition = pos
				}
			}
		}

		if bestParticipant == nil {
			break
		}

		// Insert the participant
		route.stops = insertAt(route.stops, bestParticipant, bestPosition)
		r.updateRouteDistance(ctx, route)

		// Remove from unassigned
		unassigned = removeParticipant(unassigned, bestParticipant.ID)

		log.Printf("[BALANCED] Assigned %s to %s (pos=%d, cost=%.0fm)",
			bestParticipant.Name, route.driver.Name, bestPosition, bestCost)

		// Move to next driver (round-robin)
		driverIndex = (driverIndex + 1) % len(driverIDs)
	}

	return unassigned, nil
}

// insertionCost calculates the additional distance to insert p at position pos
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
		// Inserting at end - just distance from prev to new stop
		distPrevP, err := r.distanceCalc.GetDistance(ctx, prev, p.GetCoords())
		if err != nil {
			return 0, err
		}
		return distPrevP.DistanceMeters, nil
	}

	// Cost = dist(prev, p) + dist(p, next) - dist(prev, next)
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

	return distPrevP.DistanceMeters + distPNext.DistanceMeters - distPrevNext.DistanceMeters, nil
}

// optimizeAllRoutes runs 2-opt on each route and updates distances
func (r *BalancedRouter) optimizeAllRoutes(ctx context.Context, routes map[int64]*balancedRoute) error {
	for _, route := range routes {
		if len(route.stops) >= 3 {
			optimized, err := r.twoOpt(ctx, route, route.stops)
			if err != nil {
				return err
			}
			route.stops = optimized
		}
		r.updateRouteDistance(ctx, route)
	}
	return nil
}

// twoOpt applies 2-opt optimization to reduce route distance
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
					if dist1New.DistanceMeters < dist1.DistanceMeters {
						reverse(stops, i, j-1)
						improved = true
					}
					continue
				}

				dist2, err := r.distanceCalc.GetDistance(ctx, stops[j-1].GetCoords(), afterJ)
				if err != nil {
					return nil, err
				}

				currentDist := dist1.DistanceMeters + dist2.DistanceMeters

				dist1New, err := r.distanceCalc.GetDistance(ctx, beforeI, stops[j-1].GetCoords())
				if err != nil {
					return nil, err
				}

				dist2New, err := r.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), afterJ)
				if err != nil {
					return nil, err
				}

				newDist := dist1New.DistanceMeters + dist2New.DistanceMeters

				if newDist < currentDist {
					reverse(stops, i, j-1)
					improved = true
				}
			}
		}
	}

	return stops, nil
}

// minMaxOptimize moves participants between routes to minimize the maximum route distance
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

		// Find routes sorted by distance (longest first)
		type routeDist struct {
			id   int64
			dist float64
		}
		routesByDist := make([]routeDist, 0, len(driverIDs))
		for _, id := range driverIDs {
			route := routes[id]
			dist := r.calculateRouteDistance(ctx, route)
			routesByDist = append(routesByDist, routeDist{id, dist})
		}
		sort.Slice(routesByDist, func(i, j int) bool {
			return routesByDist[i].dist > routesByDist[j].dist
		})

		currentMaxDist := routesByDist[0].dist
		if currentMaxDist == 0 {
			break
		}

		// Try to reduce routes starting from longest
		foundMove := false
		for _, rd := range routesByDist {
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
				newMaxDist      float64
			}
			bestMove.newMaxDist = currentMaxDist

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

						// Calculate new max distance
						newMaxDist := 0.0
						for _, id := range driverIDs {
							dist := r.calculateRouteDistance(ctx, routes[id])
							if dist > newMaxDist {
								newMaxDist = dist
							}
						}

						// Restore
						srcRoute.stops = oldSrcStops
						destRoute.stops = oldDestStops

						// Accept if it reduces the maximum
						if newMaxDist < bestMove.newMaxDist-10 { // 10 meter threshold
							bestMove.destID = destID
							bestMove.srcPos = srcPos
							bestMove.destPos = destPos
							bestMove.participant = p
							bestMove.newMaxDist = newMaxDist
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

				r.updateRouteDistance(ctx, srcRoute)
				r.updateRouteDistance(ctx, destRoute)

				log.Printf("[BALANCED] Moved %s from %s to %s (max: %.0fm -> %.0fm)",
					bestMove.participant.Name, srcRoute.driver.Name, destRoute.driver.Name,
					currentMaxDist, bestMove.newMaxDist)

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

// calculateRouteDistance computes distance from origin through all stops
func (r *BalancedRouter) calculateRouteDistance(ctx context.Context, route *balancedRoute) float64 {
	if len(route.stops) == 0 {
		return 0
	}

	total := 0.0
	prev := r.getOrigin(route.driver)
	for _, stop := range route.stops {
		dist, err := r.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err == nil {
			total += dist.DistanceMeters
		}
		prev = stop.GetCoords()
	}
	return total
}

// updateRouteDistance recalculates and caches the total distance for a route
func (r *BalancedRouter) updateRouteDistance(ctx context.Context, route *balancedRoute) {
	route.totalDistance = r.calculateRouteDistance(ctx, route)
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

// Note: Helper functions (insertAt, removeAt, removeParticipant, reverse)
// are defined in distance_minimizer.go and shared across routing implementations
