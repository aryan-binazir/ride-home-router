package routing

import (
	"context"
	"log"
	"math"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// distanceMinimizer implements pure distance minimization routing
type distanceMinimizer struct {
	distanceCalc distance.DistanceCalculator
}

// NewDistanceMinimizer creates a router that minimizes total driving distance
func NewDistanceMinimizer(distanceCalc distance.DistanceCalculator) Router {
	return &distanceMinimizer{
		distanceCalc: distanceCalc,
	}
}

func (r *distanceMinimizer) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	totalStart := time.Now()
	log.Printf("[DISTANCE] Starting calculation: participants=%d drivers=%d",
		len(req.Participants), len(req.Drivers))

	// Handle empty participants
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{},
			Warnings: []string{},
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
					cost, err := r.insertionCost(ctx, req.InstituteCoords, route, p, pos)
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
			optimized, err := r.twoOpt(ctx, req.InstituteCoords, route.stops)
			if err != nil {
				return nil, err
			}
			route.stops = optimized
		}
	}
	log.Printf("[TIMING] Phase 2 (2-opt): %v", time.Since(phase2Start))

	// Phase 3: Inter-route optimization - try moving participants between routes
	phase3Start := time.Now()
	iterations := r.interRouteOptimize(ctx, req.InstituteCoords, routes)
	log.Printf("[TIMING] Phase 3 (inter-route): %v (iterations=%d)", time.Since(phase3Start), iterations)

	// Phase 4: Institute vehicle fallback
	if len(unassigned) > 0 && req.InstituteVehicle != nil {
		log.Printf("[DISTANCE] Using institute vehicle for %d remaining participants", len(unassigned))
		ivRoute := &dmRoute{
			driver:             req.InstituteVehicle,
			stops:              []*models.Participant{},
			isInstituteVehicle: true,
			instituteDriverID:  req.InstituteVehicleDriverID,
		}

		// Greedy nearest-neighbor for institute vehicle
		currentLoc := req.InstituteCoords
		for len(unassigned) > 0 && len(ivRoute.stops) < req.InstituteVehicle.VehicleCapacity {
			var nearest *models.Participant
			minDist := math.Inf(1)

			for _, p := range unassigned {
				dist, err := r.distanceCalc.GetDistance(ctx, currentLoc, p.GetCoords())
				if err != nil {
					return nil, err
				}
				if dist.DistanceMeters < minDist {
					minDist = dist.DistanceMeters
					nearest = p
				}
			}

			if nearest == nil {
				break
			}

			ivRoute.stops = append(ivRoute.stops, nearest)
			currentLoc = nearest.GetCoords()
			unassigned = removeParticipant(unassigned, nearest.ID)
		}

		// 2-opt on institute vehicle route
		if len(ivRoute.stops) >= 3 {
			optimized, err := r.twoOpt(ctx, req.InstituteCoords, ivRoute.stops)
			if err != nil {
				return nil, err
			}
			ivRoute.stops = optimized
		}

		routes[req.InstituteVehicle.ID] = ivRoute
	}

	// Check for unassigned participants
	if len(unassigned) > 0 {
		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		if req.InstituteVehicle != nil {
			totalCapacity += req.InstituteVehicle.VehicleCapacity
		}
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	// Build result
	result, err := r.buildResult(ctx, req.InstituteCoords, routes, len(req.Participants))
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
	driver             *models.Driver
	stops              []*models.Participant
	isInstituteVehicle bool
	instituteDriverID  int64
}

// insertionCost calculates the additional distance to insert p at position pos
func (r *distanceMinimizer) insertionCost(ctx context.Context, institute models.Coordinates, route *dmRoute, p *models.Participant, pos int) (float64, error) {
	var prev, next models.Coordinates

	if pos == 0 {
		prev = institute
	} else {
		prev = route.stops[pos-1].GetCoords()
	}

	if pos < len(route.stops) {
		next = route.stops[pos].GetCoords()
	} else {
		// After all stops - next is just for calculation, we use driver home
		// But for pure dropoff distance, we don't count driver going home
		// So if inserting at end, cost is just distance from prev to p
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
func (r *distanceMinimizer) twoOpt(ctx context.Context, start models.Coordinates, stops []*models.Participant) ([]*models.Participant, error) {
	if len(stops) < 3 {
		return stops, nil
	}

	improved := true
	for improved {
		improved = false
		for i := 0; i < len(stops)-1; i++ {
			for j := i + 2; j <= len(stops); j++ {
				// Get coordinates for edge comparison
				var beforeI models.Coordinates
				if i == 0 {
					beforeI = start
				} else {
					beforeI = stops[i-1].GetCoords()
				}

				// Current edges: beforeI->stops[i] and stops[j-1]->afterJ
				// After reverse: beforeI->stops[j-1] and stops[i]->afterJ

				dist1, err := r.distanceCalc.GetDistance(ctx, beforeI, stops[i].GetCoords())
				if err != nil {
					return nil, err
				}

				var afterJ models.Coordinates
				if j < len(stops) {
					afterJ = stops[j].GetCoords()
				} else {
					// No next stop - compare just the first edge change
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

				// New edges after reversal
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

// interRouteOptimize tries moving participants between routes to reduce total distance
func (r *distanceMinimizer) interRouteOptimize(ctx context.Context, institute models.Coordinates, routes map[int64]*dmRoute) int {
	maxIterations := 50
	iteration := 0
	improved := true

	driverIDs := make([]int64, 0, len(routes))
	for id, route := range routes {
		if !route.isInstituteVehicle {
			driverIDs = append(driverIDs, id)
		}
	}

	for improved && iteration < maxIterations {
		improved = false
		iteration++

		// Calculate current total distance
		currentTotal, err := r.totalDropoffDistance(ctx, institute, routes)
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

						newTotal, err := r.totalDropoffDistance(ctx, institute, routes)

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

// totalDropoffDistance calculates total dropoff distance across all routes
func (r *distanceMinimizer) totalDropoffDistance(ctx context.Context, institute models.Coordinates, routes map[int64]*dmRoute) (float64, error) {
	total := 0.0

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue
		}

		prev := institute
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
func (r *distanceMinimizer) buildResult(ctx context.Context, institute models.Coordinates, routes map[int64]*dmRoute, totalParticipants int) (*models.RoutingResult, error) {
	calculatedRoutes := make([]models.CalculatedRoute, 0)
	totalDropoff := 0.0
	totalDist := 0.0
	driversUsed := 0
	usedInstituteVehicle := false

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue
		}

		driversUsed++

		// Build stops with distances
		routeStops := make([]models.RouteStop, len(route.stops))
		cumulativeDistance := 0.0
		cumulativeDuration := 0.0

		prev := institute
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

		// Distance to final destination
		destination := route.driver.GetCoords()
		if route.isInstituteVehicle {
			destination = institute
			usedInstituteVehicle = true
		}

		distToEnd, err := r.distanceCalc.GetDistance(ctx, prev, destination)
		if err != nil {
			return nil, err
		}

		// Calculate baseline (direct institute -> home)
		baseline, err := r.distanceCalc.GetDistance(ctx, institute, route.driver.GetCoords())
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
			UsedInstituteVehicle:       route.isInstituteVehicle,
			InstituteVehicleDriverID:   route.instituteDriverID,
			BaselineDurationSecs:       baseline.DurationSecs,
			RouteDurationSecs:          cumulativeDuration + distToEnd.DurationSecs,
			DetourSecs:                 (cumulativeDuration + distToEnd.DurationSecs) - baseline.DurationSecs,
		})
	}

	return &models.RoutingResult{
		Routes: calculatedRoutes,
		Summary: models.RoutingSummary{
			TotalParticipants:          totalParticipants,
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoff,
			TotalDistanceMeters:        totalDist,
			UsedInstituteVehicle:       usedInstituteVehicle,
			UnassignedParticipants:     []int64{},
		},
		Warnings: []string{},
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
