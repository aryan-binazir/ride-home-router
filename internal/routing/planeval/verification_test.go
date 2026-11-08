package planeval_test

import (
	"context"
	"fmt"
	"reflect"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"testing"
	"time"
)

func TestVerificationSmallPlanIntegrity(t *testing.T) {
	for riders := 1; riders <= 4; riders++ {
		for drivers := 1; drivers <= 3; drivers++ {
			for _, shared := range []bool{false, true} {
				for _, mode := range []routing.RouteMode{routing.RouteModeDropoff, routing.RouteModePickup} {
					t.Run(fmt.Sprintf("%d/%d/shared=%v/%s", riders, drivers, shared, mode), func(t *testing.T) {
						participants := make([]models.Participant, riders)
						expected := map[int64]int{}
						for i := range participants {
							address := fmt.Sprintf("%d Main St", i+1)
							lat := 35.95 + float64(i)*0.001
							if shared && i < 2 {
								address = "1 Main St"
								lat = 35.95
							}
							participants[i] = models.Participant{ID: int64(i + 1), Address: address, Lat: lat, Lng: -78.9}
							expected[int64(i+1)] = 1
						}
						roster := make([]models.Driver, drivers)
						seats := map[int64]int{}
						for i := range roster {
							capacity := 4 + i
							roster[i] = models.Driver{ID: int64(i + 11), VehicleCapacity: capacity, Lat: 35.96 + float64(i)*0.001, Lng: -78.91}
							seats[int64(i+11)] = capacity
						}
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						defer cancel()
						plan, err := routing.NewBalancedRouter(distance.NewEstimator()).CalculateRoutes(ctx, &routing.RoutingRequest{InstituteCoords: models.Coordinates{Lat: 35.99, Lng: -78.95}, Participants: participants, Drivers: roster, Mode: mode})
						if err != nil {
							t.Fatal(err)
						}
						if plan == nil {
							t.Fatal("missing plan")
						}
						seen := map[int64]int{}
						used := map[int64]bool{}
						householdRoute := -1
						for index, route := range plan.Routes {
							if route.Driver == nil {
								t.Fatal("route missing driver")
							}
							capacity, ok := seats[route.Driver.ID]
							if !ok || used[route.Driver.ID] {
								t.Fatalf("unknown or repeated driver %d", route.Driver.ID)
							}
							used[route.Driver.ID] = true
							if len(route.Stops) > capacity {
								t.Fatalf("driver %d carries %d with %d input seats", route.Driver.ID, len(route.Stops), capacity)
							}
							for _, stop := range route.Stops {
								if stop.Participant == nil {
									t.Fatal("missing rider")
								}
								id := stop.Participant.ID
								seen[id]++
								if shared && riders >= 2 && (id == 1 || id == 2) {
									if householdRoute >= 0 && householdRoute != index {
										t.Fatal("household split across cars")
									}
									householdRoute = index
								}
							}
						}
						if !reflect.DeepEqual(seen, expected) {
							t.Fatalf("riders=%v want %v", seen, expected)
						}
					})
				}
			}
		}
	}
}
