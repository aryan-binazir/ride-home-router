package routing

import (
	"context"
	"fmt"

	"ride-home-router/internal/models"
)

// RouteMode defines the direction of the route calculation
type RouteMode string

const (
	RouteModeDropoff RouteMode = "dropoff" // Activity Location → Participants → Driver Home
	RouteModePickup  RouteMode = "pickup"  // Driver Home → Participants → Activity Location
)

// RoutingRequest contains the input for route calculation
type RoutingRequest struct {
	InstituteCoords models.Coordinates
	Participants    []models.Participant
	Drivers         []models.Driver
	Mode            RouteMode
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
