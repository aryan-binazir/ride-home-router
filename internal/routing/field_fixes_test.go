package routing

import (
	"context"
	"errors"
	"ride-home-router/internal/models"
	"testing"
)

func TestCalculateRoutesReportsSeatShortageBeforeSearching(t *testing.T) {
	calc := &countingSolveDistanceCalculator{}
	router := NewBalancedRouter(calc)
	participants := make([]models.Participant, 5)
	for i := range participants {
		participants[i] = models.Participant{ID: int64(i + 1), Name: "Rider", Address: "1 Rider Rd", Lat: 40 + float64(i)*0.01, Lng: -74}
	}
	_, err := router.CalculateRoutes(context.Background(), &RoutingRequest{
		InstituteCoords: models.Coordinates{Lat: 40, Lng: -74},
		Participants:    participants,
		Drivers:         []models.Driver{{ID: 1, Name: "Small Car", Lat: 40.1, Lng: -74, VehicleCapacity: 3}},
		Mode:            "dropoff",
	})
	var failed *ErrRoutingFailed
	if !errors.As(err, &failed) {
		t.Fatalf("err = %v, want *ErrRoutingFailed", err)
	}
	if failed.UnassignedCount != 2 || failed.TotalCapacity != 3 || failed.TotalParticipants != 5 {
		t.Fatalf("shortage = %+v, want 2 unassigned of 5 with 3 seats", failed)
	}
	if len(calc.calls) != 0 {
		t.Fatalf("distance lookups before the shortage check = %d, want 0", len(calc.calls))
	}
}
