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

func TestHomePassBoundaries(t *testing.T) {
	venue := models.Coordinates{Lat: 0, Lng: 0}
	km := func(k float64) float64 { return k / 111.195 } // degrees of latitude
	// Home 1.99 km north of the venue->first-rider leg (inside the 2 km limit).
	home := models.Coordinates{Lat: km(1.99), Lng: 0}
	// The first rider 4.5 km east of the venue is about 4.9 km from home: not
	// "more than 5 km", so no pass even though the leg passes home.
	if passesHome(venue, home, []models.Coordinates{{Lat: 0, Lng: km(4.5)}}, false) {
		t.Fatal("a rider within 5 km of home does not make a pass")
	}
	// The venue->first-rider leg itself counts, and its destination counts as a
	// later rider: 5.0 km east of the venue is about 5.4 km from home.
	if !passesHome(venue, home, []models.Coordinates{{Lat: 0, Lng: km(5)}}, false) {
		t.Fatal("venue->first-rider leg within 2 km of home with the destination over 5 km away must count")
	}
	if passesHome(venue, models.Coordinates{Lat: km(2.05), Lng: 0}, []models.Coordinates{{Lat: 0, Lng: km(5)}}, false) {
		t.Fatal("2.05 km from the leg is outside the 2 km threshold")
	}
	// Endpoint clamp: a point beyond the segment's end measures to the endpoint.
	a, b := models.Coordinates{Lat: 0, Lng: 0}, models.Coordinates{Lat: 0, Lng: km(1)}
	beyond := models.Coordinates{Lat: 0, Lng: km(3)}
	if got := segmentDistanceKm(beyond, a, b); math.Abs(got-2) > 0.01 {
		t.Fatalf("beyond-end distance = %.3f km, want 2", got)
	}
	cross := models.Coordinates{Lat: km(1.5), Lng: km(0.5)}
	if got := segmentDistanceKm(cross, a, b); math.Abs(got-1.5) > 0.01 {
		t.Fatalf("cross-track distance = %.3f km, want 1.5", got)
	}
}
