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

	// Shuffle drivers for fairness
	drivers := make([]*models.Driver, len(req.Drivers))
	for i := range req.Drivers {
		drivers[i] = &req.Drivers[i]
	}
	rand.Shuffle(len(drivers), func(i, j int) {
		drivers[i], drivers[j] = drivers[j], drivers[i]
	})
	log.Printf("[ROUTING] Shuffled driver order: %v", driverNames(drivers))

	// Initialize route builders for each driver
	type routeBuilder struct {
		driver             *models.Driver
		stops              []*models.Participant
		isInstituteVehicle bool
		instituteDriverID  int64
	}
	builders := make([]*routeBuilder, 0, len(drivers))
	for _, d := range drivers {
		builders = append(builders, &routeBuilder{
			driver: d,
			stops:  []*models.Participant{},
		})
	}

	// Phase 1: Seeding - spread drivers geographically
	log.Printf("[ROUTING] Phase 1: Seeding drivers")
	allAssignedCoords := []models.Coordinates{}

	for i, builder := range builders {
		if len(unassigned) == 0 {
			break
		}

		var seed *models.Participant
		var err error

		if i == 0 {
			// First driver: nearest to institute
			seed, _, err = r.findNearestParticipant(ctx, req.InstituteCoords, unassigned)
		} else {
			// Subsequent drivers: farthest from all assigned participants
			seed, err = r.findFarthestFromAll(ctx, allAssignedCoords, unassigned)
		}

		if err != nil {
			return nil, err
		}
		if seed == nil {
			break
		}

		builder.stops = append(builder.stops, seed)
		allAssignedCoords = append(allAssignedCoords, seed.GetCoords())
		delete(unassigned, seed.ID)
		log.Printf("[ROUTING] Seeded driver %s with participant %s", builder.driver.Name, seed.Name)
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

	// Build final routes with distances
	var routes []models.CalculatedRoute
	for _, builder := range builders {
		if len(builder.stops) == 0 {
			continue
		}

		route, err := r.buildRouteWithDistances(ctx, builder.driver, req.InstituteCoords, builder.stops, builder.isInstituteVehicle, builder.instituteDriverID)
		if err != nil {
			return nil, err
		}
		log.Printf("[ROUTING] Final route for %s: participants=%d distance=%.0f", builder.driver.Name, len(route.Stops), route.TotalDropoffDistanceMeters)
		routes = append(routes, *route)
	}

	totalDropoffDistance := 0.0
	driversUsed := len(routes)
	usedInstituteVehicle := false
	for _, route := range routes {
		totalDropoffDistance += route.TotalDropoffDistanceMeters
		if route.UsedInstituteVehicle {
			usedInstituteVehicle = true
		}
	}

	log.Printf("[ROUTING] Calculation complete: drivers_used=%d total_distance=%.0f institute_vehicle=%v", driversUsed, totalDropoffDistance, usedInstituteVehicle)
	return &models.RoutingResult{
		Routes: routes,
		Summary: models.RoutingSummary{
			TotalParticipants:          len(req.Participants),
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoffDistance,
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
	remaining := make(map[int64]*models.Participant)
	for _, p := range participants {
		remaining[p.ID] = p
	}

	currentLocation := instituteCoords
	cumulativeDistance := 0.0

	for len(remaining) > 0 {
		nearest, distanceToNearest, err := r.findNearestParticipant(ctx, currentLocation, remaining)
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

		delete(remaining, nearest.ID)
		currentLocation = nearest.GetCoords()
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
	}

	return route, nil
}
