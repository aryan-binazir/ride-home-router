package routing

import (
	"context"
	"fmt"

	"ride-home-router/internal/models"
)

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
