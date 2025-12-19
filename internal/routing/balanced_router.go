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

// BalancedRouter implements fair distribution routing that balances load across drivers
// while still optimizing for reasonable distances
type BalancedRouter struct {
	distanceCalc    distance.DistanceCalculator
	instituteCoords models.Coordinates
	mode            RouteMode

	// FairnessWeight controls how much to prioritize fairness over pure distance
	// 0.0 = pure distance optimization (same as DistanceMinimizer)
	// 1.0 = strong preference for balanced routes
	// Default: 0.5 (balanced approach)
	FairnessWeight float64
}

// NewBalancedRouter creates a router that balances fairness with distance optimization
func NewBalancedRouter(distanceCalc distance.DistanceCalculator) Router {
	return &BalancedRouter{
		distanceCalc:   distanceCalc,
		FairnessWeight: 0.5, // Default: balanced approach
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

	log.Printf("[BALANCED] Starting calculation: participants=%d drivers=%d mode=%s fairness=%.2f",
		len(req.Participants), len(req.Drivers), r.mode, r.FairnessWeight)

	// Handle empty participants
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{},
			Warnings: []string{},
			Mode:     string(r.mode),
		}, nil
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

	// Phase 1: Balanced round-robin insertion
	phase1Start := time.Now()
	unassigned, err := r.balancedInsertion(ctx, routes, driverIDs, unassigned)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 1 (balanced insertion): %v", time.Since(phase1Start))

	// Phase 2: 2-opt on each route
	phase2Start := time.Now()
	for _, route := range routes {
		if len(route.stops) >= 3 {
			optimized, err := r.twoOpt(ctx, route, route.stops)
			if err != nil {
				return nil, err
			}
			route.stops = optimized
		}
	}
	log.Printf("[TIMING] Phase 2 (2-opt): %v", time.Since(phase2Start))

	// Phase 3: Fairness-aware inter-route optimization
	phase3Start := time.Now()
	iterations := r.fairInterRouteOptimize(ctx, routes, driverIDs)
	log.Printf("[TIMING] Phase 3 (fair inter-route): %v (iterations=%d)", time.Since(phase3Start), iterations)

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

// balancedInsertion assigns participants using round-robin with fairness penalty
func (r *BalancedRouter) balancedInsertion(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) ([]*models.Participant, error) {
	// Calculate target stops per driver for fairness reference
	totalCapacity := 0
	for _, route := range routes {
		totalCapacity += route.driver.VehicleCapacity
	}
	targetPerDriver := float64(len(unassigned)) / float64(len(driverIDs))

	log.Printf("[BALANCED] Target stops per driver: %.1f", targetPerDriver)

	// Sort drivers by ID for consistent ordering
	sort.Slice(driverIDs, func(i, j int) bool {
		return driverIDs[i] < driverIDs[j]
	})

	// Round-robin through drivers, assigning best participant to each
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

		// Find best participant for this driver (considering fairness)
		bestCost := math.Inf(1)
		var bestParticipant *models.Participant
		var bestPosition int

		// Calculate average route distance for fairness penalty
		avgDistance := r.averageRouteDistance(routes)

		for _, p := range unassigned {
			for pos := 0; pos <= len(route.stops); pos++ {
				baseCost, err := r.insertionCost(ctx, route, p, pos)
				if err != nil {
					return nil, err
				}

				// Apply fairness penalty: penalize routes that are already longer than average
				fairnessPenalty := 0.0
				if avgDistance > 0 && r.FairnessWeight > 0 {
					// How much longer is this route than average?
					routeExcess := route.totalDistance - avgDistance
					if routeExcess > 0 {
						// Penalize proportionally to how much this route exceeds average
						fairnessPenalty = r.FairnessWeight * routeExcess * 0.5
					}
				}

				adjustedCost := baseCost + fairnessPenalty

				if adjustedCost < bestCost {
					bestCost = adjustedCost
					bestParticipant = p
					bestPosition = pos
				}
			}
		}

		if bestParticipant == nil {
			break
		}

		// Insert the participant and update route distance
		insertCost, _ := r.insertionCost(ctx, route, bestParticipant, bestPosition)
		route.stops = insertAtBalanced(route.stops, bestParticipant, bestPosition)
		route.totalDistance += insertCost

		// Remove from unassigned
		unassigned = removeParticipantBalanced(unassigned, bestParticipant.ID)

		log.Printf("[BALANCED] Assigned %s to %s (pos=%d, cost=%.0fm, route_total=%.0fm)",
			bestParticipant.Name, route.driver.Name, bestPosition, insertCost, route.totalDistance)

		// Move to next driver (round-robin)
		driverIndex = (driverIndex + 1) % len(driverIDs)
	}

	return unassigned, nil
}

// averageRouteDistance calculates the mean distance across all non-empty routes
func (r *BalancedRouter) averageRouteDistance(routes map[int64]*balancedRoute) float64 {
	total := 0.0
	count := 0
	for _, route := range routes {
		if len(route.stops) > 0 {
			total += route.totalDistance
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
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
		// Inserting at end
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
						reverseBalanced(stops, i, j-1)
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
					reverseBalanced(stops, i, j-1)
					improved = true
				}
			}
		}
	}

	return stops, nil
}

// fairInterRouteOptimize tries moving participants between routes considering fairness
func (r *BalancedRouter) fairInterRouteOptimize(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64) int {
	maxIterations := 50
	iteration := 0
	improved := true

	for improved && iteration < maxIterations {
		improved = false
		iteration++

		// Calculate current metrics
		currentTotal, err := r.totalDropoffDistance(ctx, routes)
		if err != nil {
			break
		}

		// Calculate current fairness score (lower is better = more balanced)
		currentFairness := r.calculateFairnessScore(routes)

		// Find the best move considering both distance and fairness
		var bestMove struct {
			srcID, destID   int64
			srcPos, destPos int
			participant     *models.Participant
			score           float64 // Combined score (negative = improvement)
		}
		bestMove.score = 0 // Must improve to be accepted

		for i := 0; i < len(driverIDs); i++ {
			for j := 0; j < len(driverIDs); j++ {
				if i == j {
					continue
				}

				srcID, destID := driverIDs[i], driverIDs[j]
				srcRoute, destRoute := routes[srcID], routes[destID]

				if len(srcRoute.stops) == 0 {
					continue
				}
				if len(destRoute.stops) >= destRoute.driver.VehicleCapacity {
					continue
				}

				// Try moving each participant from src to dest
				for srcPos := 0; srcPos < len(srcRoute.stops); srcPos++ {
					p := srcRoute.stops[srcPos]

					for destPos := 0; destPos <= len(destRoute.stops); destPos++ {
						// Simulate the move
						newSrcStops := removeAtBalanced(srcRoute.stops, srcPos)
						newDestStops := insertAtBalanced(destRoute.stops, p, destPos)

						oldSrcStops := srcRoute.stops
						oldDestStops := destRoute.stops
						srcRoute.stops = newSrcStops
						destRoute.stops = newDestStops

						newTotal, err := r.totalDropoffDistance(ctx, routes)
						newFairness := r.calculateFairnessScore(routes)

						// Restore
						srcRoute.stops = oldSrcStops
						destRoute.stops = oldDestStops

						if err != nil {
							continue
						}

						// Calculate combined improvement score
						// Negative values indicate improvement
						distanceChange := newTotal - currentTotal
						fairnessChange := newFairness - currentFairness

						// Weight the improvements
						// Distance: raw meters saved
						// Fairness: scale to be comparable (multiply by typical route distance)
						avgDist := currentTotal / float64(len(driverIDs))
						if avgDist < 1 {
							avgDist = 1000 // Default if no routes yet
						}

						combinedScore := distanceChange + (r.FairnessWeight * fairnessChange * avgDist)

						// Accept if it improves the combined score
						// Threshold: must improve by at least 10 meters equivalent
						if combinedScore < bestMove.score-10 {
							bestMove.srcID = srcID
							bestMove.destID = destID
							bestMove.srcPos = srcPos
							bestMove.destPos = destPos
							bestMove.participant = p
							bestMove.score = combinedScore
						}
					}
				}
			}
		}

		// Execute the best move if found
		if bestMove.participant != nil {
			srcRoute := routes[bestMove.srcID]
			destRoute := routes[bestMove.destID]

			srcRoute.stops = removeAtBalanced(srcRoute.stops, bestMove.srcPos)
			destRoute.stops = insertAtBalanced(destRoute.stops, bestMove.participant, bestMove.destPos)

			// Update cached distances
			r.updateRouteDistance(ctx, srcRoute)
			r.updateRouteDistance(ctx, destRoute)

			log.Printf("[BALANCED] Moved %s from %s to %s (combined_improvement=%.0f)",
				bestMove.participant.Name, srcRoute.driver.Name, destRoute.driver.Name, -bestMove.score)
			improved = true
		}
	}

	return iteration
}

// calculateFairnessScore returns a score where lower = more balanced
// Uses standard deviation of route distances
func (r *BalancedRouter) calculateFairnessScore(routes map[int64]*balancedRoute) float64 {
	distances := make([]float64, 0, len(routes))
	for _, route := range routes {
		if len(route.stops) > 0 {
			distances = append(distances, route.totalDistance)
		}
	}

	if len(distances) <= 1 {
		return 0
	}

	// Calculate mean
	sum := 0.0
	for _, d := range distances {
		sum += d
	}
	mean := sum / float64(len(distances))

	// Calculate standard deviation
	variance := 0.0
	for _, d := range distances {
		diff := d - mean
		variance += diff * diff
	}
	variance /= float64(len(distances))

	return math.Sqrt(variance)
}

// updateRouteDistance recalculates the total distance for a route
func (r *BalancedRouter) updateRouteDistance(ctx context.Context, route *balancedRoute) {
	if len(route.stops) == 0 {
		route.totalDistance = 0
		return
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
	route.totalDistance = total
}

// totalDropoffDistance calculates total dropoff distance across all routes
func (r *BalancedRouter) totalDropoffDistance(ctx context.Context, routes map[int64]*balancedRoute) (float64, error) {
	total := 0.0

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue
		}

		prev := r.getOrigin(route.driver)
		for _, stop := range route.stops {
			dist, err := r.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
			if err != nil {
				return 0, err
			}
			total += dist.DistanceMeters
			prev = stop.GetCoords()
		}
	}

	return total, nil
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

// Helper functions
func insertAtBalanced(stops []*models.Participant, p *models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)+1)
	copy(result[:pos], stops[:pos])
	result[pos] = p
	copy(result[pos+1:], stops[pos:])
	return result
}

func removeAtBalanced(stops []*models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)-1)
	copy(result[:pos], stops[:pos])
	copy(result[pos:], stops[pos+1:])
	return result
}

func removeParticipantBalanced(stops []*models.Participant, id int64) []*models.Participant {
	result := make([]*models.Participant, 0, len(stops)-1)
	for _, p := range stops {
		if p.ID != id {
			result = append(result, p)
		}
	}
	return result
}

func reverseBalanced(stops []*models.Participant, i, j int) {
	for i < j {
		stops[i], stops[j] = stops[j], stops[i]
		i++
		j--
	}
}
