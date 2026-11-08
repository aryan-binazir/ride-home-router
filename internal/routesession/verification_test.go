package routesession_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"testing"
)

func TestVerificationBoundedMoveIntegrity(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []models.RouteMode{models.RouteModeDropoff, models.RouteModePickup} {
		for first := range 27 {
			for second := range 27 {
				t.Run(fmt.Sprintf("%s/%d/%d", mode, first, second), func(t *testing.T) {
					s := routesession.NewStore(distance.NewEstimator())
					t.Cleanup(s.Close)
					drivers := []models.Driver{{ID: 1, VehicleCapacity: 2}, {ID: 2, VehicleCapacity: 2}, {ID: 3, VehicleCapacity: 2}}
					routes := make([]models.CalculatedRoute, 3)
					for i := range routes {
						p := &models.Participant{ID: int64(i + 1), Address: fmt.Sprintf("%d street", i), Lat: float64(i+1) / 100}
						routes[i] = models.CalculatedRoute{Driver: &drivers[i], EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: p}}}
					}
					snap, err := s.CreateContext(ctx, routesession.CreateInput{Routes: routes, SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{}, Mode: mode})
					if err != nil {
						t.Fatal(err)
					}
					for _, code := range []int{first, second} {
						p := int64(code/9 + 1)
						dest := (code / 3) % 3
						pos := code%3 - 1

						snap, err = s.ApplyMoves(ctx, snap.ID, []routesession.Move{{ParticipantID: p, ToRouteIndex: dest, InsertAtPosition: pos}}, routesession.ApplyMovesOptions{})
						if err != nil {
							t.Fatal(err)
						}
						counts := map[int64]int{}
						ds := map[int64]int{}
						for _, r := range snap.Routes {
							ds[r.Driver.ID]++
							for _, stop := range r.Stops {
								counts[stop.Participant.ID]++
							}
						}
						if !reflect.DeepEqual(counts, map[int64]int{1: 1, 2: 1, 3: 1}) || !reflect.DeepEqual(ds, map[int64]int{1: 1, 2: 1, 3: 1}) {
							t.Fatalf("mode=%s commands=%d,%d counts=%v drivers=%v", mode, first, second, counts, ds)
						}

						_, err = s.ApplyMoves(ctx, snap.ID, []routesession.Move{{ParticipantID: p, ToRouteIndex: (dest + 1) % 3}, {ParticipantID: 999, ToRouteIndex: 0}}, routesession.ApplyMovesOptions{})
						if !errors.Is(err, routesession.ErrParticipantNotFound) {
							t.Fatal(err)
						}
						after, ok, loadErr := s.Load(ctx, snap.ID)
						if loadErr != nil || !ok {
							t.Fatalf("load after rollback: found=%v err=%v", ok, loadErr)
						}
						if !reflect.DeepEqual(after.Routes, snap.Routes) {
							t.Fatalf("failed batch changed state; mode=%s commands=%d,%d", mode, first, second)
						}
					}
				})
			}
		}
	}
}
