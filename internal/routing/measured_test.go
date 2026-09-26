package routing

import (
	"context"
	"errors"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
	"testing"
)

const (
	fakeLegMeters = 100
	fakeLegSecs   = 60
)

type fakeMeasurer struct {
	requests []orderedroute.Request
	fail     map[string]error
}

func (f *fakeMeasurer) Measure(_ context.Context, requests []orderedroute.Request) []orderedroute.Result {
	f.requests = append(f.requests, requests...)
	results := make([]orderedroute.Result, len(requests))
	for i, request := range requests {
		results[i].ID = request.ID
		if err, ok := f.fail[request.ID]; ok {
			results[i].Err = err
			continue
		}
		for range len(request.Points) - 1 {
			results[i].Legs = append(results[i].Legs, orderedroute.Leg{DistanceMeters: fakeLegMeters, DurationSecs: fakeLegSecs})
		}
	}
	return results
}

func measuredFixture() (models.Coordinates, []models.CalculatedRoute) {
	institute := models.Coordinates{Lat: 42.0, Lng: -71.0}
	driver := &models.Driver{ID: 7, Name: "Dana", Address: "9 Driver Rd", Lat: 42.1, Lng: -71.1, VehicleCapacity: 4}
	siblingA := &models.Participant{ID: 1, Name: "Ana", Address: "1 Main St", Lat: 42.01, Lng: -71.01}
	siblingB := &models.Participant{ID: 2, Name: "Ben", Address: "1 Main St", Lat: 42.01, Lng: -71.01}
	solo := &models.Participant{ID: 3, Name: "Cy", Address: "5 Oak Ave", Lat: 42.02, Lng: -71.02}
	routes := []models.CalculatedRoute{
		{Driver: driver, EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: siblingA}, {Participant: siblingB}, {Participant: solo}}},
		{Driver: &models.Driver{ID: 8, Name: "Empty", Lat: 42.2, Lng: -71.2}},
	}
	return institute, routes
}

func TestMeasureRoutesCollapsesHouseholdsAndAssemblesMetrics(t *testing.T) {
	institute, routes := measuredFixture()
	measurer := &fakeMeasurer{}
	original := routes[0].Stops[0].CumulativeDurationSecs
	results := MeasureRoutes(context.Background(), measurer, institute, RouteModeDropoff, routes, []int{0})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("MeasureRoutes() = %+v", results)
	}
	if len(measurer.requests) != 2 {
		t.Fatalf("requests = %d, want route + baseline", len(measurer.requests))
	}
	var routeReq, baseReq orderedroute.Request
	for _, request := range measurer.requests {
		if len(request.Points) == 4 {
			routeReq = request
		} else {
			baseReq = request
		}
	}
	if routeReq.Points[0] != institute || routeReq.Points[3] != (models.Coordinates{Lat: 42.1, Lng: -71.1}) {
		t.Fatalf("dropoff route must run institute → stops → driver home: %+v", routeReq.Points)
	}
	if len(baseReq.Points) != 2 || baseReq.Points[0] != institute {
		t.Fatalf("baseline must be institute → driver home: %+v", baseReq.Points)
	}
	route := results[0].Route
	if route.Stops[0].DurationFromPrevSecs != 60 || route.Stops[0].CumulativeDurationSecs != 60 {
		t.Fatalf("first sibling metrics = %+v", route.Stops[0])
	}
	if route.Stops[1].DurationFromPrevSecs != 0 || route.Stops[1].CumulativeDurationSecs != 60 || route.Stops[1].DistanceFromPrevMeters != 0 {
		t.Fatalf("second sibling should ride the same stop: %+v", route.Stops[1])
	}
	if route.Stops[2].CumulativeDurationSecs != 120 || route.Stops[2].CumulativeDistanceMeters != 200 {
		t.Fatalf("solo stop metrics = %+v", route.Stops[2])
	}
	if route.TotalDropoffDistanceMeters != 200 || route.DistanceToDriverHomeMeters != 100 || route.TotalDistanceMeters != 300 {
		t.Fatalf("distances = %+v", route)
	}
	if route.RouteDurationSecs != 180 || route.BaselineDurationSecs != 60 || route.DetourSecs != 120 {
		t.Fatalf("durations = route %v baseline %v detour %v", route.RouteDurationSecs, route.BaselineDurationSecs, route.DetourSecs)
	}
	if route.Mode != RouteModeDropoff || route.Stops[2].Order != 2 {
		t.Fatalf("mode/order not populated: %+v", route)
	}
	if routes[0].Stops[0].CumulativeDurationSecs != original {
		t.Fatal("input routes must not be mutated")
	}
}

func TestMeasureRoutesPickupRunsHomeToInstituteAndSkipsEmptyCars(t *testing.T) {
	institute, routes := measuredFixture()
	measurer := &fakeMeasurer{}
	results := MeasureRoutes(context.Background(), measurer, institute, RouteModePickup, routes, []int{0, 1})
	if len(results) != 2 {
		t.Fatalf("results = %d", len(results))
	}
	if results[1].Err != nil || len(results[1].Route.Stops) != 0 || results[1].Route.RouteDurationSecs != 0 {
		t.Fatalf("empty car should measure as zero without requests: %+v", results[1])
	}
	if len(measurer.requests) != 2 {
		t.Fatalf("requests = %d, want only the occupied car's route and baseline", len(measurer.requests))
	}
	for _, request := range measurer.requests {
		if request.Points[0] != (models.Coordinates{Lat: 42.1, Lng: -71.1}) || request.Points[len(request.Points)-1] != institute {
			t.Fatalf("pickup must run driver home → stops → institute: %+v", request.Points)
		}
	}
}

func TestMeasureRoutesReportsPerCarFailures(t *testing.T) {
	institute, routes := measuredFixture()
	boom := errors.New("provider down")
	measurer := &fakeMeasurer{fail: map[string]error{"route-0": boom}}
	results := MeasureRoutes(context.Background(), measurer, institute, RouteModeDropoff, routes, []int{0})
	if !errors.Is(results[0].Err, boom) {
		t.Fatalf("err = %v, want the provider error", results[0].Err)
	}
	measurer = &fakeMeasurer{fail: map[string]error{"baseline-0": boom}}
	results = MeasureRoutes(context.Background(), measurer, institute, RouteModeDropoff, routes, []int{0})
	if !errors.Is(results[0].Err, boom) {
		t.Fatalf("baseline failure must fail the car: %v", results[0].Err)
	}
}
