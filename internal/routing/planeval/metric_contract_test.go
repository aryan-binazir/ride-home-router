package planeval

import (
	"math"
	"ride-home-router/internal/models"
	"testing"
)

func TestMetricContractGreatCircleSegment(t *testing.T) {
	// The shortest segment crosses the date line and passes through home.
	home := models.Coordinates{Lat: 0, Lng: 180}
	a := models.Coordinates{Lat: 0, Lng: 179}
	b := models.Coordinates{Lat: 0, Lng: -179}
	if got := segmentDistanceKm(home, a, b); got > 1e-7 {
		t.Fatalf("distance = %v km, want zero", got)
	}
	if got := segmentDistanceKm(home, a, a); math.Abs(got-haversineKm(home, a)) > 1e-7 {
		t.Fatalf("coincident endpoint distance = %v", got)
	}
}

func TestMetricContractMalformedStop(t *testing.T) {
	p := models.Participant{ID: 1}
	plan := &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, VehicleCapacity: 4}, Stops: []models.RouteStop{{Participant: &p}, {}}}}}
	if err := validPlan(plan, Roster{Participants: []models.Participant{p}}); err == nil {
		t.Fatal("accepted a missing participant, which halves measured burden")
	}
}

func TestMetricContractUnroundedNearestRankAndGate(t *testing.T) {
	var routes []models.CalculatedRoute
	for i := range 28 {
		burden := float64(i)
		if i == 26 {
			burden = 26.004
		}
		p := &models.Participant{ID: int64(i + 1), Lat: float64(i) / 1000}
		routes = append(routes, models.CalculatedRoute{Driver: &models.Driver{ID: int64(i + 1), Lat: float64(i) / 1000}, DetourSecs: burden * 60, Stops: []models.RouteStop{{Participant: p}}})
	}
	measured := Measure(&models.RoutingResult{Routes: routes}, models.Coordinates{}, 0)
	if math.Abs(measured.BurdenP95Min-26.004) > 1e-10 || measured.BurdenMaxMin != 27 {
		t.Fatalf("burden = %v/%v", measured.BurdenP95Min, measured.BurdenMaxMin)
	}
	baseline := measured
	baseline.BurdenP95Min = 25
	if len(Compare([]Result{{Key: "test", Metrics: baseline}}, []Result{{Key: "test", Metrics: measured}})) != 1 {
		t.Fatal("rounded away a p95 gate failure")
	}
	if m := Measure(&models.RoutingResult{}, models.Coordinates{}, 0); m.BurdenP95Min != 0 || m.BurdenMaxMin != 0 {
		t.Fatal("empty burden must be zero")
	}
}
