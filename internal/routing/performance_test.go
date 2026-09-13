package routing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"testing"
)

// Distances are generated before measurement and are deliberately asymmetric.
// Rounded points, tied costs and shared households exercise the public solver.
type warmDistances struct {
	indexes map[models.Coordinates]int
	values  []distance.DistanceResult
}

func (s warmDistances) GetDistance(ctx context.Context, a, b models.Coordinates) (*distance.DistanceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	i, okA := s.indexes[a]
	j, okB := s.indexes[b]
	if !okA || !okB {
		return nil, fmt.Errorf("fixture distance is missing")
	}
	v := s.values[i*len(s.indexes)+j]
	return &v, nil
}

func (s warmDistances) PrewarmPairs(ctx context.Context, pairs []distance.DistancePair) error {
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := s.indexes[pair.Origin]; !ok {
			return fmt.Errorf("fixture origin is missing")
		}
		if _, ok := s.indexes[pair.Destination]; !ok {
			return fmt.Errorf("fixture destination is missing")
		}
	}
	return nil
}

func performanceFixture(n, drivers int, households bool) (routing.RoutingRequest, warmDistances) {
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // Reproducible synthetic data.
	req := routing.RoutingRequest{InstituteCoords: models.Coordinates{Lat: 35.9536, Lng: -78.9772}}
	points := []models.Coordinates{req.InstituteCoords}
	for i := range n + drivers {
		point := models.Coordinates{Lat: models.RoundCoordinate(35.9 + rng.Float64()*.11), Lng: models.RoundCoordinate(-79.07 + rng.Float64()*.19)}
		if i < n {
			address := fmt.Sprintf("%d Test Lane", i+1)
			if households && i%2 == 1 {
				point = req.Participants[i-1].GetCoords()
				address = req.Participants[i-1].Address
			}
			req.Participants = append(req.Participants, models.Participant{ID: int64(i + 1), Name: fmt.Sprintf("P%d", i+1), Address: address, Lat: point.Lat, Lng: point.Lng})
		} else {
			// Deliberately unsorted driver IDs preserve the initial request-order case.
			req.Drivers = append(req.Drivers, models.Driver{ID: int64(n + drivers - i), Name: fmt.Sprintf("D%d", i-n+1), Lat: point.Lat, Lng: point.Lng, VehicleCapacity: (n+drivers-1)/drivers + 2})
		}
		points = append(points, point)
	}
	source := warmDistances{indexes: make(map[models.Coordinates]int)}
	unique := make([]models.Coordinates, 0, len(points))
	for _, p := range points {
		if _, exists := source.indexes[p]; !exists {
			source.indexes[p] = len(unique)
			unique = append(unique, p)
		}
	}
	source.values = make([]distance.DistanceResult, len(unique)*len(unique))
	for i, a := range unique {
		for j, b := range unique {
			if i == j {
				continue
			}
			meters := math.Round(math.Hypot((a.Lat-b.Lat)*111000, (a.Lng-b.Lng)*90000))
			source.values[i*len(unique)+j] = distance.DistanceResult{DistanceMeters: meters, DurationSecs: math.Round(meters/12) + float64((i*7+j*3)%11)}
		}
	}
	return req, source
}

func discardRoutingLogs(t testing.TB) {
	t.Helper()
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
}

func TestRoutingPreservesReferenceResults(t *testing.T) {
	discardRoutingLogs(t)
	for _, mode := range []routing.RouteMode{routing.RouteModeDropoff, routing.RouteModePickup} {
		for _, households := range []bool{false, true} {
			name := fmt.Sprintf("%s-households-%t", mode, households)
			t.Run(name, func(t *testing.T) {
				req, source := performanceFixture(12, 3, households)
				req.Mode = mode
				result, err := routing.NewBalancedRouter(source).CalculateRoutes(t.Context(), &req)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join("testdata", "reference-"+name+".json")

				expected, err := os.ReadFile(path) //nolint:gosec // Path uses only fixed test-case names.
				if err != nil {
					t.Fatal(err)
				}
				if string(actual) != string(expected) {
					t.Fatalf("route assignments or exact metrics differ from 82c6384: got %s", actual)
				}
				for i := range result.Routes {
					if err := routing.OptimizeRouteOrder(t.Context(), source, req.InstituteCoords, mode, &result.Routes[i]); err != nil {
						t.Fatal(err)
					}
				}
				actual, err = json.MarshalIndent(result, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join("testdata", "edited-"+name+".json")

				expected, err = os.ReadFile(path) //nolint:gosec // Path uses only fixed test-case names.
				if err != nil {
					t.Fatal(err)
				}
				if string(actual) != string(expected) {
					t.Fatalf("edited route metrics differ from 82c6384: got %s", actual)
				}
			})
		}
	}
}

func BenchmarkCalculateRoutesWarm(b *testing.B) {
	discardRoutingLogs(b)
	for _, n := range []int{40, 100, 1000} {
		for _, mode := range []routing.RouteMode{routing.RouteModeDropoff, routing.RouteModePickup} {
			b.Run(fmt.Sprintf("%d/%s", n, mode), func(b *testing.B) {
				req, source := performanceFixture(n, n/5, false)
				req.Mode = mode
				router := routing.NewBalancedRouter(source)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := router.CalculateRoutes(b.Context(), &req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

type textualDistances map[string]distance.DistanceResult

func (s textualDistances) GetDistance(ctx context.Context, a, b models.Coordinates) (*distance.DistanceResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, ok := s[distance.PairCacheKey(a, b)]
	if !ok {
		return nil, fmt.Errorf("unexpected directed pair")
	}
	return &value, nil
}

func (textualDistances) PrewarmPairs(ctx context.Context, _ []distance.DistancePair) error {
	return ctx.Err()
}

func TestCalculationPreservesRoundedDistanceIdentity(t *testing.T) {
	discardRoutingLogs(t)
	for _, tc := range []struct {
		name                          string
		participant, driver, baseline float64
	}{
		{"signed zero", -0.000001, 0.000001, 7},
		{"rounding tie", 0.000005, 0.000006, 11},
		{"negative rounding tie", -0.000005, -0.000006, 11},
		{"infinity", math.Inf(1), math.Inf(1), 11},
		{"NaN payloads", math.Float64frombits(0x7ff8000000000001), math.Float64frombits(0x7ff8000000000002), 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := models.Coordinates{Lat: 35.95, Lng: -78.98}
			p := models.Coordinates{Lat: tc.participant, Lng: 0}
			d := models.Coordinates{Lat: tc.driver, Lng: 0}
			source := textualDistances{
				distance.PairCacheKey(a, d): {DurationSecs: tc.baseline},
				distance.PairCacheKey(p, d): {},
			}
			source[distance.PairCacheKey(a, p)] = distance.DistanceResult{DurationSecs: 11}
			req := routing.RoutingRequest{InstituteCoords: a, Participants: []models.Participant{{ID: 1, Lat: p.Lat, Lng: p.Lng}}, Drivers: []models.Driver{{ID: 2, Lat: d.Lat, Lng: d.Lng, VehicleCapacity: 1}}}
			result, err := routing.NewBalancedRouter(source).CalculateRoutes(t.Context(), &req)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Routes) != 1 {
				t.Fatalf("routes = %d", len(result.Routes))
			}
			route := result.Routes[0]
			if route.RouteDurationSecs != 11 || route.BaselineDurationSecs != tc.baseline || route.DetourSecs != 11-tc.baseline {
				t.Fatalf("rounded directed costs changed: duration=%v baseline=%v detour=%v", route.RouteDurationSecs, route.BaselineDurationSecs, route.DetourSecs)
			}
		})
	}
}
