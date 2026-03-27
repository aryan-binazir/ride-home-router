package routing

import (
	"context"
	"log"
	"math"
	"slices"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// distanceMinimizer implements pure distance minimization routing
type distanceMinimizer struct {
	distanceCalc distance.DistanceCalculator
}

// NewDistanceMinimizer creates a router that minimizes total driving distance.
func NewDistanceMinimizer(distanceCalc distance.DistanceCalculator) Router {
	return &distanceMinimizer{
		distanceCalc: distanceCalc,
	}
}

func (r *distanceMinimizer) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	totalStart := time.Now()

	rc := newRouteContext(r.distanceCalc, req.InstituteCoords, req.Mode)

	log.Printf("[DISTANCE] Starting calculation: participants=%d drivers=%d mode=%s",
		len(req.Participants), len(req.Drivers), rc.mode)

	// Handle empty participants
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{},
			Warnings: []string{},
			Mode:     string(rc.mode),
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
	routes := make(map[int64]*dmRoute)
	for i := range req.Drivers {
		driver := &req.Drivers[i]
		routes[driver.ID] = &dmRoute{
			driver: driver,
			stops:  []*models.Participant{},
		}
	}

	// Build unassigned list
	unassigned := make([]*models.Participant, len(req.Participants))
	for i := range req.Participants {
		unassigned[i] = &req.Participants[i]
	}

	// Phase 1: Cheapest insertion - assign each participant where it adds least distance
	phase1Start := time.Now()
	for len(unassigned) > 0 {
		bestCost := math.Inf(1)
		var bestDriverID int64
		var bestParticipant *models.Participant
		var bestPosition int

		for _, p := range unassigned {
			for driverID, route := range routes {
				if len(route.stops) >= route.driver.VehicleCapacity {
					continue
				}

				// Find best position to insert this participant
				for pos := 0; pos <= len(route.stops); pos++ {
					cost, err := rc.insertionDeltaDistance(ctx, route.driver, route.stops, p, pos)
					if err != nil {
						return nil, err
					}

					if cost < bestCost {
						bestCost = cost
						bestDriverID = driverID
						bestParticipant = p
						bestPosition = pos
					}
				}
			}
		}

		if bestParticipant == nil {
			// All drivers full
			break
		}

		// Insert the participant
		route := routes[bestDriverID]
		route.stops = insertAt(route.stops, bestParticipant, bestPosition)

		// Remove from unassigned
		unassigned = removeParticipant(unassigned, bestParticipant.ID)

		log.Printf("[DISTANCE] Assigned %s to %s (pos=%d, cost=%.0fm)",
			bestParticipant.Name, route.driver.Name, bestPosition, bestCost)
	}
	log.Printf("[TIMING] Phase 1 (insertion): %v", time.Since(phase1Start))

	// Phase 2: 2-opt on each route
	phase2Start := time.Now()
	for _, route := range routes {
		if len(route.stops) >= 3 {
			optimized, err := r.twoOpt(ctx, rc, route, route.stops)
			if err != nil {
				return nil, err
			}
			route.stops = optimized
		}
	}
	log.Printf("[TIMING] Phase 2 (2-opt): %v", time.Since(phase2Start))

	// Phase 3: Inter-route optimization - try moving participants between routes
	phase3Start := time.Now()
	iterations := r.interRouteOptimize(ctx, rc, routes)
	log.Printf("[TIMING] Phase 3 (inter-route): %v (iterations=%d)", time.Since(phase3Start), iterations)

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
	result, err := r.buildResult(ctx, rc, routes, len(req.Participants))
	if err != nil {
		return nil, err
	}

	log.Printf("[DISTANCE] Complete: drivers_used=%d total_distance=%.0fm",
		result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)
	log.Printf("[TIMING] TOTAL: %v", time.Since(totalStart))

	return result, nil
}

// dmRoute is a simple route structure
type dmRoute struct {
	driver *models.Driver
	stops  []*models.Participant
}

// twoOpt applies 2-opt optimization to reduce the routing objective.
func (r *distanceMinimizer) twoOpt(ctx context.Context, rc routeContext, route *dmRoute, stops []*models.Participant) ([]*models.Participant, error) {
	return rc.twoOptDistance(ctx, route.driver, stops)
}

// interRouteOptimize tries moving participants between routes to reduce total objective distance.
func (r *distanceMinimizer) interRouteOptimize(ctx context.Context, rc routeContext, routes map[int64]*dmRoute) int {
	maxIterations := 50
	iteration := 0
	improved := true

	driverIDs := make([]int64, 0, len(routes))
	for id := range routes {
		driverIDs = append(driverIDs, id)
	}

	for improved && iteration < maxIterations {
		improved = false
		iteration++

		// Calculate current total distance
		currentTotal, err := r.totalRouteDistance(ctx, rc, routes)
		if err != nil {
			break
		}

		// Find the single best move across all route pairs
		var bestMove struct {
			srcID, destID   int64
			srcPos, destPos int
			participant     *models.Participant
			saving          float64
		}

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

					// Find best position in dest
					for destPos := 0; destPos <= len(destRoute.stops); destPos++ {
						// Simulate the move
						newSrcStops := removeAt(srcRoute.stops, srcPos)
						newDestStops := insertAt(destRoute.stops, p, destPos)

						// Calculate new total
						oldSrcStops := srcRoute.stops
						oldDestStops := destRoute.stops
						srcRoute.stops = newSrcStops
						destRoute.stops = newDestStops

						newTotal, err := r.totalRouteDistance(ctx, rc, routes)

						// Always restore after simulation
						srcRoute.stops = oldSrcStops
						destRoute.stops = oldDestStops

						if err != nil {
							continue
						}

						saving := currentTotal - newTotal
						if saving > bestMove.saving {
							bestMove.srcID = srcID
							bestMove.destID = destID
							bestMove.srcPos = srcPos
							bestMove.destPos = destPos
							bestMove.participant = p
							bestMove.saving = saving
						}
					}
				}
			}
		}

		// Execute the best move if it improves things
		if bestMove.saving > 1 { // 1 meter threshold
			srcRoute := routes[bestMove.srcID]
			destRoute := routes[bestMove.destID]

			srcRoute.stops = removeAt(srcRoute.stops, bestMove.srcPos)
			destRoute.stops = insertAt(destRoute.stops, bestMove.participant, bestMove.destPos)

			log.Printf("[DISTANCE] Moved %s from %s to %s (saved %.0fm)",
				bestMove.participant.Name, srcRoute.driver.Name, destRoute.driver.Name, bestMove.saving)
			improved = true
		}
	}

	return iteration
}

// totalRouteDistance calculates the optimization objective across all routes.
func (r *distanceMinimizer) totalRouteDistance(ctx context.Context, rc routeContext, routes map[int64]*dmRoute) (float64, error) {
	total := 0.0

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue
		}

		routeTotal, err := rc.routeDistance(ctx, route.driver, route.stops)
		if err != nil {
			return 0, err
		}
		total += routeTotal
	}

	return total, nil
}

// buildResult creates the final routing result
func (r *distanceMinimizer) buildResult(ctx context.Context, rc routeContext, routes map[int64]*dmRoute, totalParticipants int) (*models.RoutingResult, error) {
	calculatedRoutes := make([]models.CalculatedRoute, 0)
	totalDropoff := 0.0
	totalDist := 0.0
	driversUsed := 0

	driverIDs := make([]int64, 0, len(routes))
	for id := range routes {
		driverIDs = append(driverIDs, id)
	}
	slices.Sort(driverIDs)

	for _, driverID := range driverIDs {
		route := routes[driverID]
		if len(route.stops) == 0 {
			continue
		}

		driversUsed++

		// Build stops with distances
		routeStops := make([]models.RouteStop, len(route.stops))
		metrics, err := rc.evaluateParticipants(ctx, route.driver, route.stops)
		if err != nil {
			return nil, err
		}
		for i, p := range route.stops {
			routeStops[i] = models.RouteStop{
				Order:                    i,
				Participant:              p,
				DistanceFromPrevMeters:   metrics.Stops[i].DistanceFromPrevMeters,
				CumulativeDistanceMeters: metrics.Stops[i].CumulativeDistanceMeters,
				DurationFromPrevSecs:     metrics.Stops[i].DurationFromPrevSecs,
				CumulativeDurationSecs:   metrics.Stops[i].CumulativeDurationSecs,
			}
		}

		totalDropoff += metrics.TotalStopDistanceMeters
		totalDist += metrics.TotalDistanceMeters

		calculatedRoutes = append(calculatedRoutes, models.CalculatedRoute{
			Driver:                     route.driver,
			Stops:                      routeStops,
			TotalDropoffDistanceMeters: metrics.TotalStopDistanceMeters,
			DistanceToDriverHomeMeters: metrics.FinalLegDistanceMeters,
			TotalDistanceMeters:        metrics.TotalDistanceMeters,
			EffectiveCapacity:          route.driver.VehicleCapacity,
			BaselineDurationSecs:       metrics.BaselineDurationSecs,
			RouteDurationSecs:          metrics.RouteDurationSecs,
			DetourSecs:                 metrics.DetourSecs,
			Mode:                       string(rc.mode),
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
		Mode:     string(rc.mode),
	}, nil
}

// Helper functions
func insertAt(stops []*models.Participant, p *models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)+1)
	copy(result[:pos], stops[:pos])
	result[pos] = p
	copy(result[pos+1:], stops[pos:])
	return result
}

func removeAt(stops []*models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)-1)
	copy(result[:pos], stops[:pos])
	copy(result[pos:], stops[pos+1:])
	return result
}

func removeParticipant(stops []*models.Participant, id int64) []*models.Participant {
	result := make([]*models.Participant, 0, len(stops)-1)
	for _, p := range stops {
		if p.ID != id {
			result = append(result, p)
		}
	}
	return result
}

func reverse(stops []*models.Participant, i, j int) {
	for i < j {
		stops[i], stops[j] = stops[j], stops[i]
		i++
		j--
	}
}
