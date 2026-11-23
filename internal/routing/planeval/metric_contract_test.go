package planeval

import (
	"math"
	"ride-home-router/internal/models"
	"testing"
)

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
