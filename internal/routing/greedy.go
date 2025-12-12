package routing

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// RoutingRequest contains the input for route calculation
type RoutingRequest struct {
	InstituteCoords          models.Coordinates
	Participants             []models.Participant
	Drivers                  []models.Driver
	InstituteVehicle         *models.Driver
	InstituteVehicleDriverID int64
}

// Router provides route optimization
type Router interface {
	CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error)
}

// ErrRoutingFailed is returned when no valid route solution exists
type ErrRoutingFailed struct {
	Reason            string
	UnassignedCount   int
	TotalCapacity     int
	TotalParticipants int
}

func (e *ErrRoutingFailed) Error() string {
	return fmt.Sprintf("routing failed: %s", e.Reason)
}

type greedyRouter struct {
	distanceCalc distance.DistanceCalculator
}

type routeBuilder struct {
	driver             *models.Driver
	stops              []*models.Participant
	isInstituteVehicle bool
	instituteDriverID  int64
}

// NewGreedyRouter creates a new greedy nearest-neighbor router
func NewGreedyRouter(distanceCalc distance.DistanceCalculator) Router {
	return &greedyRouter{
		distanceCalc: distanceCalc,
	}
}

func (r *greedyRouter) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	log.Printf("[ROUTING] Starting calculation: participants=%d drivers=%d institute_vehicle=%v", len(req.Participants), len(req.Drivers), req.InstituteVehicle != nil)

	if len(req.Participants) == 0 {
		log.Printf("[ROUTING] No participants to route")
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{TotalParticipants: 0, TotalDriversUsed: 0},
			Warnings: []string{},
		}, nil
	}

	// Prewarm distance cache
	allPoints := []models.Coordinates{req.InstituteCoords}
	for _, p := range req.Participants {
		allPoints = append(allPoints, p.GetCoords())
	}
	for _, d := range req.Drivers {
		allPoints = append(allPoints, d.GetCoords())
	}
	if req.InstituteVehicle != nil {
		allPoints = append(allPoints, req.InstituteVehicle.GetCoords())
	}

	if err := r.distanceCalc.PrewarmCache(ctx, allPoints); err != nil {
		return nil, err
	}

	// Build unassigned map
	unassigned := make(map[int64]*models.Participant)
	for i := range req.Participants {
		unassigned[req.Participants[i].ID] = &req.Participants[i]
	}

	// Shuffle drivers for fairness (randomizes tie-breaking)
	drivers := make([]*models.Driver, len(req.Drivers))
	for i := range req.Drivers {
		drivers[i] = &req.Drivers[i]
	}
	rand.Shuffle(len(drivers), func(i, j int) {
		drivers[i], drivers[j] = drivers[j], drivers[i]
	})
	log.Printf("[ROUTING] Shuffled driver order: %v", driverNames(drivers))

	// Initialize route builders for each driver
	builders := make([]*routeBuilder, 0, len(drivers))
	for _, d := range drivers {
		builders = append(builders, &routeBuilder{
			driver: d,
			stops:  []*models.Participant{},
		})
	}

	// Phase 1: Seeding - spread drivers geographically, considering driver homes
	log.Printf("[ROUTING] Phase 1: Seeding drivers (with home-aware assignment)")

	// Step 1: Collect spread-out seeds using farthest-from-all
	numSeeds := len(builders)
	if numSeeds > len(unassigned) {
		numSeeds = len(unassigned)
	}

	seeds := make([]*models.Participant, 0, numSeeds)
	seedCoords := []models.Coordinates{}

	for i := 0; i < numSeeds; i++ {
		var seed *models.Participant
		var err error

		if i == 0 {
			// First seed: nearest to institute
			seed, _, err = r.findNearestParticipant(ctx, req.InstituteCoords, unassigned)
		} else {
			// Subsequent seeds: farthest from all already-selected seeds
			seed, err = r.findFarthestFromAll(ctx, seedCoords, unassigned)
		}

		if err != nil {
			return nil, err
		}
		if seed == nil {
			break
		}

		seeds = append(seeds, seed)
		seedCoords = append(seedCoords, seed.GetCoords())
		delete(unassigned, seed.ID)
	}

	// Step 2: Assign seeds to drivers from the seed's perspective
	// Each seed goes to the driver whose home is closest to it (avoids driver A stealing seed B really needs)
	assignedDrivers := make(map[int]bool)
	for seedIdx, seed := range seeds {
		bestBuilderIdx := -1
		bestDist := -1.0

		for builderIdx, builder := range builders {
			if assignedDrivers[builderIdx] {
				continue
			}

			// Distance from seed to this driver's home
			dist, err := r.distanceCalc.GetDistance(ctx, seed.GetCoords(), builder.driver.GetCoords())
			if err != nil {
				return nil, err
			}

			if bestDist < 0 || dist.DistanceMeters < bestDist {
				bestDist = dist.DistanceMeters
				bestBuilderIdx = builderIdx
			}
		}

		if bestBuilderIdx >= 0 {
			builders[bestBuilderIdx].stops = append(builders[bestBuilderIdx].stops, seed)
			assignedDrivers[bestBuilderIdx] = true
			log.Printf("[ROUTING] Seeded driver %s with participant %s (seed %d, %.0fm from driver home)",
				builders[bestBuilderIdx].driver.Name, seed.Name, seedIdx+1, bestDist)
		}
	}

	// Phase 2: Greedy clustering - each driver picks nearest to their cluster
	log.Printf("[ROUTING] Phase 2: Greedy clustering")
	for len(unassigned) > 0 {
		assigned := false

		for _, builder := range builders {
			if len(unassigned) == 0 {
				break
			}
			if len(builder.stops) >= builder.driver.VehicleCapacity {
				continue // Driver is full
			}
			if len(builder.stops) == 0 {
				continue // Driver didn't get a seed (more drivers than participants)
			}

			// Find nearest to any of this driver's current stops
			nearest, _, err := r.findNearestToAnyStop(ctx, builder.stops, unassigned)
			if err != nil {
				return nil, err
			}
			if nearest == nil {
				continue
			}

			builder.stops = append(builder.stops, nearest)
			delete(unassigned, nearest.ID)
			assigned = true
		}

		if !assigned {
			break // No driver could take anyone
		}
	}

	// Handle remaining with institute vehicle if available
	if len(unassigned) > 0 && req.InstituteVehicle != nil {
		log.Printf("[ROUTING] Using institute vehicle for %d remaining participants", len(unassigned))
		instituteBuilder := &routeBuilder{
			driver:             req.InstituteVehicle,
			stops:              []*models.Participant{},
			isInstituteVehicle: true,
			instituteDriverID:  req.InstituteVehicleDriverID,
		}

		// Assign remaining using nearest-neighbor from institute
		currentLoc := req.InstituteCoords
		for len(unassigned) > 0 && len(instituteBuilder.stops) < req.InstituteVehicle.VehicleCapacity {
			nearest, _, err := r.findNearestParticipant(ctx, currentLoc, unassigned)
			if err != nil {
				return nil, err
			}
			if nearest == nil {
				break
			}
			instituteBuilder.stops = append(instituteBuilder.stops, nearest)
			currentLoc = nearest.GetCoords()
			delete(unassigned, nearest.ID)
		}

		if len(instituteBuilder.stops) > 0 {
			builders = append(builders, instituteBuilder)
		}
	}

	// Check if we still have unassigned
	if len(unassigned) > 0 {
		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		if req.InstituteVehicle != nil {
			totalCapacity += req.InstituteVehicle.VehicleCapacity
		}

		log.Printf("[ERROR] Routing failed: unassigned=%d total_capacity=%d total_participants=%d", len(unassigned), totalCapacity, len(req.Participants))
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants to available drivers",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	// Phase 3: Build routes with ordering and 2-opt
	log.Printf("[ROUTING] Phase 3: Building ordered routes with 2-opt")
	routes := make([]models.CalculatedRoute, 0, len(builders))
	for _, builder := range builders {
		if len(builder.stops) == 0 {
			continue
		}

		route, err := r.buildRouteWithDistances(ctx, builder.driver, req.InstituteCoords, builder.stops, builder.isInstituteVehicle, builder.instituteDriverID)
		if err != nil {
			return nil, err
		}
		log.Printf("[ROUTING] Built route for %s: participants=%d distance=%.0f", builder.driver.Name, len(route.Stops), route.TotalDropoffDistanceMeters)
		routes = append(routes, *route)
	}

	// Phase 4: Inter-route boundary optimization (now working on ordered routes)
	log.Printf("[ROUTING] Phase 4: Inter-route boundary swaps")
	routes, err := r.interRouteOptimizeOrdered(ctx, req.InstituteCoords, routes)
	if err != nil {
		return nil, err
	}

	totalDropoffDistance := 0.0
	totalDistance := 0.0
	driversUsed := len(routes)
	usedInstituteVehicle := false
	for _, route := range routes {
		totalDropoffDistance += route.TotalDropoffDistanceMeters
		totalDistance += route.TotalDistanceMeters
		if route.UsedInstituteVehicle {
			usedInstituteVehicle = true
		}
	}

	log.Printf("[ROUTING] Calculation complete: drivers_used=%d dropoff_distance=%.0f total_distance=%.0f institute_vehicle=%v", driversUsed, totalDropoffDistance, totalDistance, usedInstituteVehicle)
	return &models.RoutingResult{
		Routes: routes,
		Summary: models.RoutingSummary{
			TotalParticipants:          len(req.Participants),
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoffDistance,
			TotalDistanceMeters:        totalDistance,
			UsedInstituteVehicle:       usedInstituteVehicle,
			UnassignedParticipants:     []int64{},
		},
		Warnings: []string{},
	}, nil
}

func driverNames(drivers []*models.Driver) []string {
	names := make([]string, len(drivers))
	for i, d := range drivers {
		names[i] = d.Name
	}
	return names
}

func (r *greedyRouter) assignDriverRoute(
	ctx context.Context,
	driver *models.Driver,
	instituteCoords models.Coordinates,
	unassigned map[int64]*models.Participant,
	isInstituteVehicle bool,
	instituteVehicleDriverID int64,
) (*models.CalculatedRoute, error) {
	route := &models.CalculatedRoute{
		Driver:                   driver,
		Stops:                    []models.RouteStop{},
		UsedInstituteVehicle:     isInstituteVehicle,
		InstituteVehicleDriverID: instituteVehicleDriverID,
	}

	currentLocation := instituteCoords
	cumulativeDistance := 0.0

	for len(route.Stops) < driver.VehicleCapacity && len(unassigned) > 0 {
		nearest, distanceToNearest, err := r.findNearestParticipant(ctx, currentLocation, unassigned)
		if err != nil {
			return nil, err
		}

		if nearest == nil {
			break
		}

		cumulativeDistance += distanceToNearest

		route.Stops = append(route.Stops, models.RouteStop{
			Order:                    len(route.Stops),
			Participant:              nearest,
			DistanceFromPrevMeters:   distanceToNearest,
			CumulativeDistanceMeters: cumulativeDistance,
		})

		delete(unassigned, nearest.ID)
		currentLocation = nearest.GetCoords()
	}

	route.TotalDropoffDistanceMeters = cumulativeDistance

	if len(route.Stops) > 0 {
		if isInstituteVehicle {
			distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, instituteCoords)
			if err != nil {
				return nil, err
			}
			route.DistanceToDriverHomeMeters = distResult.DistanceMeters
		} else {
			distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, driver.GetCoords())
			if err != nil {
				return nil, err
			}
			route.DistanceToDriverHomeMeters = distResult.DistanceMeters
		}
	}

	return route, nil
}

func (r *greedyRouter) findNearestParticipant(
	ctx context.Context,
	currentLocation models.Coordinates,
	unassigned map[int64]*models.Participant,
) (*models.Participant, float64, error) {
	if len(unassigned) == 0 {
		return nil, 0, nil
	}

	var nearest *models.Participant
	minDistance := -1.0

	for _, participant := range unassigned {
		distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, participant.GetCoords())
		if err != nil {
			return nil, 0, err
		}

		if minDistance < 0 || distResult.DistanceMeters < minDistance {
			minDistance = distResult.DistanceMeters
			nearest = participant
		}
	}

	return nearest, minDistance, nil
}

// findFarthestFromAll finds the participant farthest from all given coordinates
func (r *greedyRouter) findFarthestFromAll(
	ctx context.Context,
	assignedCoords []models.Coordinates,
	unassigned map[int64]*models.Participant,
) (*models.Participant, error) {
	if len(unassigned) == 0 {
		return nil, nil
	}

	var farthest *models.Participant
	maxMinDistance := -1.0

	for _, participant := range unassigned {
		// Find minimum distance from this participant to any assigned coord
		minDistToAssigned := -1.0
		for _, coord := range assignedCoords {
			distResult, err := r.distanceCalc.GetDistance(ctx, coord, participant.GetCoords())
			if err != nil {
				return nil, err
			}
			if minDistToAssigned < 0 || distResult.DistanceMeters < minDistToAssigned {
				minDistToAssigned = distResult.DistanceMeters
			}
		}

		// We want the participant whose minimum distance is the largest
		if minDistToAssigned > maxMinDistance {
			maxMinDistance = minDistToAssigned
			farthest = participant
		}
	}

	return farthest, nil
}

// findNearestToAnyStop finds the participant nearest to any of the driver's current stops
func (r *greedyRouter) findNearestToAnyStop(
	ctx context.Context,
	stops []*models.Participant,
	unassigned map[int64]*models.Participant,
) (*models.Participant, float64, error) {
	if len(unassigned) == 0 || len(stops) == 0 {
		return nil, 0, nil
	}

	var nearest *models.Participant
	minDistance := -1.0

	for _, participant := range unassigned {
		// Find minimum distance from this participant to any stop
		for _, stop := range stops {
			distResult, err := r.distanceCalc.GetDistance(ctx, stop.GetCoords(), participant.GetCoords())
			if err != nil {
				return nil, 0, err
			}
			if minDistance < 0 || distResult.DistanceMeters < minDistance {
				minDistance = distResult.DistanceMeters
				nearest = participant
			}
		}
	}

	return nearest, minDistance, nil
}

// buildRouteWithDistances creates a final route with proper stop ordering and distances
func (r *greedyRouter) buildRouteWithDistances(
	ctx context.Context,
	driver *models.Driver,
	instituteCoords models.Coordinates,
	participants []*models.Participant,
	isInstituteVehicle bool,
	instituteVehicleDriverID int64,
) (*models.CalculatedRoute, error) {
	route := &models.CalculatedRoute{
		Driver:                   driver,
		Stops:                    []models.RouteStop{},
		UsedInstituteVehicle:     isInstituteVehicle,
		InstituteVehicleDriverID: instituteVehicleDriverID,
	}

	if len(participants) == 0 {
		return route, nil
	}

	// Order stops using nearest-neighbor from institute
	ordered := make([]*models.Participant, 0, len(participants))
	remaining := make(map[int64]*models.Participant)
	for _, p := range participants {
		remaining[p.ID] = p
	}

	currentLocation := instituteCoords
	for len(remaining) > 0 {
		nearest, _, err := r.findNearestParticipant(ctx, currentLocation, remaining)
		if err != nil {
			return nil, err
		}
		if nearest == nil {
			break
		}
		ordered = append(ordered, nearest)
		delete(remaining, nearest.ID)
		currentLocation = nearest.GetCoords()
	}

	// Apply 2-opt optimization
	ordered, err := r.twoOptOptimize(ctx, instituteCoords, ordered)
	if err != nil {
		return nil, err
	}

	// Build final route with distances
	cumulativeDistance := 0.0
	currentLocation = instituteCoords
	for i, p := range ordered {
		distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, p.GetCoords())
		if err != nil {
			return nil, err
		}
		cumulativeDistance += distResult.DistanceMeters
		route.Stops = append(route.Stops, models.RouteStop{
			Order:                    i,
			Participant:              p,
			DistanceFromPrevMeters:   distResult.DistanceMeters,
			CumulativeDistanceMeters: cumulativeDistance,
		})
		currentLocation = p.GetCoords()
	}

	route.TotalDropoffDistanceMeters = cumulativeDistance

	// Calculate distance to driver's home (or back to institute for institute vehicle)
	if len(route.Stops) > 0 {
		lastStop := route.Stops[len(route.Stops)-1].Participant.GetCoords()
		var destination models.Coordinates
		if isInstituteVehicle {
			destination = instituteCoords
		} else {
			destination = driver.GetCoords()
		}

		distResult, err := r.distanceCalc.GetDistance(ctx, lastStop, destination)
		if err != nil {
			return nil, err
		}
		route.DistanceToDriverHomeMeters = distResult.DistanceMeters
		route.TotalDistanceMeters = route.TotalDropoffDistanceMeters + route.DistanceToDriverHomeMeters
	}

	return route, nil
}

// interRouteOptimizeOrdered optimizes routes after they've been ordered
// Works on the actual geographic boundaries (last stop in ordered route)
func (r *greedyRouter) interRouteOptimizeOrdered(
	ctx context.Context,
	instituteCoords models.Coordinates,
	routes []models.CalculatedRoute,
) ([]models.CalculatedRoute, error) {
	if len(routes) < 2 {
		return routes, nil
	}

	improved := true
	iterations := 0
	maxIterations := 50

	for improved && iterations < maxIterations {
		improved = false
		iterations++

		for i := 0; i < len(routes); i++ {
			for j := i + 1; j < len(routes); j++ {
				// Try relocating last stop from route i to route j
				if len(routes[i].Stops) > 1 && len(routes[j].Stops) < routes[j].Driver.VehicleCapacity {
					newRoutes, didImprove, err := r.tryRelocateLastOrdered(ctx, instituteCoords, routes, i, j)
					if err != nil {
						return nil, err
					}
					if didImprove {
						routes = newRoutes
						improved = true
						log.Printf("[ROUTING] Relocated last stop from %s to %s", routes[i].Driver.Name, routes[j].Driver.Name)
					}
				}

				// Try relocating last stop from route j to route i
				if len(routes[j].Stops) > 1 && len(routes[i].Stops) < routes[i].Driver.VehicleCapacity {
					newRoutes, didImprove, err := r.tryRelocateLastOrdered(ctx, instituteCoords, routes, j, i)
					if err != nil {
						return nil, err
					}
					if didImprove {
						routes = newRoutes
						improved = true
						log.Printf("[ROUTING] Relocated last stop from %s to %s", routes[j].Driver.Name, routes[i].Driver.Name)
					}
				}

				// Try swapping last stops between routes
				if len(routes[i].Stops) >= 1 && len(routes[j].Stops) >= 1 {
					newRoutes, didImprove, err := r.trySwapLastOrdered(ctx, instituteCoords, routes, i, j)
					if err != nil {
						return nil, err
					}
					if didImprove {
						routes = newRoutes
						improved = true
						log.Printf("[ROUTING] Swapped last stops between %s and %s", routes[i].Driver.Name, routes[j].Driver.Name)
					}
				}
			}
		}
	}

	if iterations > 1 {
		log.Printf("[ROUTING] Inter-route optimization completed after %d iterations", iterations)
	}

	return routes, nil
}

// tryRelocateLastOrdered moves the last stop from routes[srcIdx] to routes[destIdx]
// Returns modified routes slice, whether improvement was made, and any error
func (r *greedyRouter) tryRelocateLastOrdered(
	ctx context.Context,
	instituteCoords models.Coordinates,
	routes []models.CalculatedRoute,
	srcIdx, destIdx int,
) ([]models.CalculatedRoute, bool, error) {
	srcRoute := &routes[srcIdx]
	destRoute := &routes[destIdx]

	if len(srcRoute.Stops) < 2 {
		return routes, false, nil
	}

	currentTotal := srcRoute.TotalDropoffDistanceMeters + destRoute.TotalDropoffDistanceMeters

	// Extract participants for rebuilding
	lastStop := srcRoute.Stops[len(srcRoute.Stops)-1]

	srcParticipants := make([]*models.Participant, len(srcRoute.Stops)-1)
	for i := 0; i < len(srcRoute.Stops)-1; i++ {
		srcParticipants[i] = srcRoute.Stops[i].Participant
	}

	destParticipants := make([]*models.Participant, len(destRoute.Stops)+1)
	for i := 0; i < len(destRoute.Stops); i++ {
		destParticipants[i] = destRoute.Stops[i].Participant
	}
	destParticipants[len(destRoute.Stops)] = lastStop.Participant

	// Rebuild both routes with 2-opt
	newSrc, err := r.buildRouteWithDistances(ctx, srcRoute.Driver, instituteCoords, srcParticipants, srcRoute.UsedInstituteVehicle, srcRoute.InstituteVehicleDriverID)
	if err != nil {
		return nil, false, err
	}
	newDest, err := r.buildRouteWithDistances(ctx, destRoute.Driver, instituteCoords, destParticipants, destRoute.UsedInstituteVehicle, destRoute.InstituteVehicleDriverID)
	if err != nil {
		return nil, false, err
	}

	newTotal := newSrc.TotalDropoffDistanceMeters + newDest.TotalDropoffDistanceMeters

	if newTotal < currentTotal {
		routes[srcIdx] = *newSrc
		routes[destIdx] = *newDest
		return routes, true, nil
	}

	return routes, false, nil
}

// trySwapLastOrdered swaps the last stops between routes[idxA] and routes[idxB]
func (r *greedyRouter) trySwapLastOrdered(
	ctx context.Context,
	instituteCoords models.Coordinates,
	routes []models.CalculatedRoute,
	idxA, idxB int,
) ([]models.CalculatedRoute, bool, error) {
	routeA := &routes[idxA]
	routeB := &routes[idxB]

	if len(routeA.Stops) < 1 || len(routeB.Stops) < 1 {
		return routes, false, nil
	}

	currentTotal := routeA.TotalDropoffDistanceMeters + routeB.TotalDropoffDistanceMeters

	lastA := routeA.Stops[len(routeA.Stops)-1].Participant
	lastB := routeB.Stops[len(routeB.Stops)-1].Participant

	// Build new participant lists with swapped last stops
	newAParticipants := make([]*models.Participant, len(routeA.Stops))
	for i := 0; i < len(routeA.Stops)-1; i++ {
		newAParticipants[i] = routeA.Stops[i].Participant
	}
	newAParticipants[len(routeA.Stops)-1] = lastB

	newBParticipants := make([]*models.Participant, len(routeB.Stops))
	for i := 0; i < len(routeB.Stops)-1; i++ {
		newBParticipants[i] = routeB.Stops[i].Participant
	}
	newBParticipants[len(routeB.Stops)-1] = lastA

	// Rebuild both routes with 2-opt
	newA, err := r.buildRouteWithDistances(ctx, routeA.Driver, instituteCoords, newAParticipants, routeA.UsedInstituteVehicle, routeA.InstituteVehicleDriverID)
	if err != nil {
		return nil, false, err
	}
	newB, err := r.buildRouteWithDistances(ctx, routeB.Driver, instituteCoords, newBParticipants, routeB.UsedInstituteVehicle, routeB.InstituteVehicleDriverID)
	if err != nil {
		return nil, false, err
	}

	newTotal := newA.TotalDropoffDistanceMeters + newB.TotalDropoffDistanceMeters

	if newTotal < currentTotal {
		routes[idxA] = *newA
		routes[idxB] = *newB
		return routes, true, nil
	}

	return routes, false, nil
}

// twoOptOptimize improves route ordering by reversing segments that reduce total distance
func (r *greedyRouter) twoOptOptimize(
	ctx context.Context,
	start models.Coordinates,
	stops []*models.Participant,
) ([]*models.Participant, error) {
	if len(stops) < 3 {
		return stops, nil // 2-opt needs at least 3 stops to be meaningful
	}

	improved := true
	for improved {
		improved = false
		for i := 0; i < len(stops)-1; i++ {
			for j := i + 2; j < len(stops); j++ {
				// Calculate current distance for edges (i-1 to i) and (j to j+1)
				// vs reversed: (i-1 to j) and (i to j+1)
				var fromI, toJ1 models.Coordinates
				if i == 0 {
					fromI = start
				} else {
					fromI = stops[i-1].GetCoords()
				}
				toJ1 = stops[j].GetCoords()

				// Current: fromI -> stops[i] and stops[j] -> next
				currentDist, err := r.distanceCalc.GetDistance(ctx, fromI, stops[i].GetCoords())
				if err != nil {
					return nil, err
				}

				// New: fromI -> stops[j] (after reversal)
				newDist, err := r.distanceCalc.GetDistance(ctx, fromI, toJ1)
				if err != nil {
					return nil, err
				}

				// Also need to consider the edge after j
				var afterJ models.Coordinates
				if j < len(stops)-1 {
					afterJ = stops[j+1].GetCoords()
					// Current: stops[j] -> afterJ
					currAfter, err := r.distanceCalc.GetDistance(ctx, stops[j].GetCoords(), afterJ)
					if err != nil {
						return nil, err
					}
					currentDist.DistanceMeters += currAfter.DistanceMeters

					// New: stops[i] -> afterJ (after reversal, i becomes the new end of reversed segment)
					newAfter, err := r.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), afterJ)
					if err != nil {
						return nil, err
					}
					newDist.DistanceMeters += newAfter.DistanceMeters
				}

				if newDist.DistanceMeters < currentDist.DistanceMeters {
					// Reverse segment from i to j
					for left, right := i, j; left < right; left, right = left+1, right-1 {
						stops[left], stops[right] = stops[right], stops[left]
					}
					improved = true
				}
			}
		}
	}

	return stops, nil
}
