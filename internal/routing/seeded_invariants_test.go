package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"runtime"
	"slices"
	"testing"
)

func TestBalancedRouterSeededInvariants(t *testing.T) {
	discardRoutingLogs(t)
	previous := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	run := func(seed uint64, households, capacities []int) {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("household sizes %v, car capacities %v", households, capacities)
				}
			})
			for _, mode := range []routing.RouteMode{routing.RouteModePickup, routing.RouteModeDropoff} {
				t.Run(string(mode), func(t *testing.T) {
					runtime.GOMAXPROCS(1)
					req := seededRosterRequest(seed, households, capacities, mode)
					router := routing.NewBalancedRouter(distance.NewEstimator())
					first, firstErr := router.CalculateRoutes(t.Context(), &req)
					checkSeededRoster(t, req, households, capacities, first, firstErr)
					want, err := json.Marshal(first)
					if err != nil {
						t.Fatal(err)
					}

					runtime.GOMAXPROCS(4)
					req = seededRosterRequest(seed, households, capacities, mode)
					second, secondErr := router.CalculateRoutes(t.Context(), &req)
					got, err := json.Marshal(second)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != string(want) || !reflect.DeepEqual(secondErr, firstErr) {
						t.Fatalf("GOMAXPROCS=1 gave %s, %v; GOMAXPROCS=4 gave %s, %v", want, firstErr, got, secondErr)
					}
				})
			}
		})
	}

	run(200, []int{6, 5, 3}, []int{6, 5, 3})
	run(201, []int{6, 5, 3}, []int{6, 5, 2})
	run(202, []int{6, 2}, []int{4, 3, 1})
	run(203, []int{2, 2, 2}, []int{3, 3})
	run(205, []int{2, 2, 1}, []int{4, 1, 1})
	for seed := range uint64(180) {
		rng := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // Seeded test data.
		householdLimit, capacityLimit := 6, 10
		if seed >= 90 {
			householdLimit, capacityLimit = 3, 4
		}
		var households []int
		for remaining := int(seed % 15); remaining > 0; {
			size := 1 + rng.IntN(min(remaining, householdLimit))
			households = append(households, size)
			remaining -= size
		}
		capacities := make([]int, 1+int(seed/15%6))
		for i := range capacities {
			capacities[i] = 1 + rng.IntN(capacityLimit)
		}
		run(seed, households, capacities)
	}
	for offset := range uint64(24) {
		seed := 300 + offset
		rng := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // Seeded test data.
		largestCar := 1 + rng.IntN(4)
		oversizedHousehold := largestCar + 1 + rng.IntN(min(largestCar, 6-largestCar))
		intactHousehold := 1 + rng.IntN(largestCar)
		capacities := []int{largestCar, largestCar, intactHousehold}
		if offset%2 == 0 {
			capacities = append(capacities, 1+rng.IntN(largestCar))
		}
		run(seed, []int{oversizedHousehold, intactHousehold}, capacities)
	}
}

func seededRosterRequest(seed uint64, households, capacities []int, mode routing.RouteMode) routing.RoutingRequest {
	rng := rand.New(rand.NewPCG(seed, 2)) //nolint:gosec // Seeded test data.
	venue := models.Coordinates{Lat: 35.99, Lng: -78.95}
	req := routing.RoutingRequest{InstituteCoords: venue, Mode: mode}
	points := []models.Coordinates{venue, {Lat: 35.98, Lng: -78.94}, {Lat: 36.01, Lng: -78.93}}
	for i, size := range households {
		point := points[rng.IntN(len(points))]
		address := fmt.Sprintf("%d Test Lane", i+1)
		for range size {
			req.Participants = append(req.Participants, models.Participant{
				ID: int64(len(req.Participants) + 1), Address: address, Lat: point.Lat, Lng: point.Lng,
			})
		}
	}
	for i, capacity := range capacities {
		point := points[rng.IntN(len(points))]
		req.Drivers = append(req.Drivers, models.Driver{
			ID: int64(i + 1), VehicleCapacity: capacity, Lat: point.Lat, Lng: point.Lng,
		})
	}
	rng.Shuffle(len(req.Participants), func(i, j int) { req.Participants[i], req.Participants[j] = req.Participants[j], req.Participants[i] })
	rng.Shuffle(len(req.Drivers), func(i, j int) { req.Drivers[i], req.Drivers[j] = req.Drivers[j], req.Drivers[i] })
	return req
}

func checkSeededRoster(t *testing.T, req routing.RoutingRequest, households, capacities []int, plan *models.RoutingResult, err error) {
	t.Helper()
	seats := 0
	for _, capacity := range capacities {
		seats += capacity
	}
	riders := len(req.Participants)
	largestCar := slices.Max(capacities)
	checkOccupancy := riders <= 7 && (len(households) == 0 || slices.Max(households) <= largestCar)
	feasible, best := false, 0
	if riders <= seats {
		feasible, best = exhaustiveRosterPacking(households, capacities, checkOccupancy)
	}
	if !feasible {
		failure, ok := errors.AsType[*routing.ErrRoutingFailed](err)
		if plan != nil || !ok {
			t.Fatalf("infeasible roster returned plan %v and error %v, want nil plan and ErrRoutingFailed", plan, err)
		}
		if failure.TotalCapacity != seats || failure.TotalParticipants != riders {
			t.Fatalf("routing failure = %+v, want %d seats and %d riders", *failure, seats, riders)
		}
		if shortage := riders - seats; shortage > 0 && failure.UnassignedCount != shortage {
			t.Fatalf("unassigned count = %d, want seat shortage %d", failure.UnassignedCount, shortage)
		}
		if failure.UnassignedCount < 1 || failure.UnassignedCount > riders {
			t.Fatalf("unassigned count = %d, want between 1 and %d", failure.UnassignedCount, riders)
		}
		return
	}
	if err != nil || plan == nil {
		t.Fatalf("feasible roster returned plan %v and error %v", plan, err)
	}
	if plan.Mode != req.Mode || plan.Summary.TotalParticipants != riders || plan.Summary.TotalDriversUsed != len(plan.Routes) || len(plan.Summary.UnassignedParticipants) != 0 {
		t.Fatalf("mode=%s summary=%+v, want mode=%s, %d riders, %d drivers and no unassigned riders", plan.Mode, plan.Summary, req.Mode, riders, len(plan.Routes))
	}

	driverSeats := make(map[int64]int, len(req.Drivers))
	for _, driver := range req.Drivers {
		driverSeats[driver.ID] = driver.VehicleCapacity
	}
	riderAddress := make(map[int64]string, riders)
	householdSize := make(map[string]int)
	for _, rider := range req.Participants {
		riderAddress[rider.ID] = rider.Address
		householdSize[rider.Address]++
	}
	householdDriver := make(map[string]int64)
	for _, route := range plan.Routes {
		if route.Driver == nil {
			t.Fatal("route has no driver")
		}
		driverID := route.Driver.ID
		capacity := driverSeats[driverID]
		if capacity == 0 || len(route.Stops) == 0 || len(route.Stops) > capacity {
			t.Fatalf("driver %d has %d riders and %d available input seats", driverID, len(route.Stops), capacity)
		}
		delete(driverSeats, driverID)
		lastStop := make(map[string]int)
		for i, stop := range route.Stops {
			if stop.Participant == nil {
				t.Fatalf("driver %d has a missing rider", driverID)
			}
			id := stop.Participant.ID
			address, ok := riderAddress[id]
			if !ok {
				t.Fatalf("unknown or repeated rider %d", id)
			}
			delete(riderAddress, id)
			if previous, seen := householdDriver[address]; seen && previous != driverID && householdSize[address] <= largestCar {
				t.Fatalf("household %q fits one car but was split between drivers %d and %d", address, previous, driverID)
			}
			householdDriver[address] = driverID
			if previous, seen := lastStop[address]; seen && previous != i-1 {
				t.Fatalf("household %q has separated stops with driver %d", address, driverID)
			}
			lastStop[address] = i
		}
	}
	if len(riderAddress) != 0 {
		t.Fatalf("riders missing from routes: %v", riderAddress)
	}
	if checkOccupancy && len(plan.Routes) != best {
		t.Fatalf("used %d drivers, exhaustive packing can use %d; stops by route: %v", len(plan.Routes), best, stopOrders(plan))
	}
}

func exhaustiveRosterPacking(households, capacities []int, checkOccupancy bool) (feasible bool, maxOccupied int) {
	largestCar := slices.Max(capacities)
	var blocks []int
	for _, size := range households {
		if checkOccupancy || size > 1 && size <= largestCar {
			blocks = append(blocks, size)
		}
	}
	slices.SortFunc(blocks, func(a, b int) int { return b - a })
	free := slices.Clone(capacities)
	target := 0
	if checkOccupancy {
		target = min(len(blocks), len(capacities))
	}
	var search func(int, int)
	search = func(block, occupied int) {
		if feasible && maxOccupied == target {
			return
		}
		if block == len(blocks) {
			feasible = true
			if checkOccupancy {
				maxOccupied = max(maxOccupied, occupied)
			}
			return
		}
		for i, seats := range free {
			if seats < blocks[block] {
				continue
			}
			next := occupied
			if seats == capacities[i] {
				next++
			}
			free[i] -= blocks[block]
			search(block+1, next)
			free[i] = seats
		}
	}
	search(0, 0)
	return feasible, maxOccupied
}

func TestBalancedRouterPreCanceledEstimator(t *testing.T) {
	discardRoutingLogs(t)
	req := seededRosterRequest(204, []int{2, 1}, []int{2, 1}, routing.RouteModePickup)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan, err := routing.NewBalancedRouter(distance.NewEstimator()).CalculateRoutes(ctx, &req)
	if plan != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled solve returned plan %v and error %v, want nil plan and context.Canceled", plan, err)
	}
}
