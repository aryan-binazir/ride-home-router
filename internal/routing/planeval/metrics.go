package planeval

import (
	"math"
	"ride-home-router/internal/models"
)

// Metrics is what a coordinator would care about in a plan, in plain units.
type Metrics struct {
	CarsUsed         int     `json:"cars_used"`
	TotalDistanceKm  float64 `json:"total_distance_km"`
	MaxDetourMin     float64 `json:"max_detour_min"`
	AverageDetourMin float64 `json:"average_detour_min"`
	LongestRiderMin  float64 `json:"longest_rider_min"` // longest time any rider spends in a car
	FarDrivers       int     `json:"far_drivers"`       // driver home over 15 km from their riders' centre
	BacktrackingCars int     `json:"backtracking_cars"` // a later stop undoes over 2 km of progress
	SplitHouseholds  int     `json:"split_households"`
	SolveMs          int64   `json:"solve_ms"`
}

// Measure summarises a plan. Distances and times are the planner's own
// estimates, so they compare like with like across planner versions.
func Measure(result *models.RoutingResult, venue models.Coordinates, solveMs int64) Metrics {
	m := Metrics{SolveMs: solveMs}
	homes := map[string]map[int64]struct{}{}
	for i, route := range result.Routes {
		if len(route.Stops) == 0 {
			continue
		}
		m.CarsUsed++
		m.TotalDistanceKm += route.TotalDistanceMeters / 1000
		m.MaxDetourMin = math.Max(m.MaxDetourMin, route.DetourSecs/60)
		m.AverageDetourMin += route.DetourSecs / 60
		var lat, lng float64
		var radii []float64
		for _, stop := range route.Stops {
			if stop.Participant == nil {
				continue
			}
			rider := stop.CumulativeDurationSecs
			if result.Mode == models.RouteModePickup {
				rider = route.RouteDurationSecs - stop.CumulativeDurationSecs
			}
			m.LongestRiderMin = math.Max(m.LongestRiderMin, rider/60)
			lat += stop.Participant.Lat
			lng += stop.Participant.Lng
			radii = append(radii, haversineKm(venue, stop.Participant.GetCoords()))
			key := stop.Participant.Address
			if homes[key] == nil {
				homes[key] = map[int64]struct{}{}
			}
			homes[key][int64(i)] = struct{}{}
		}
		n := float64(len(route.Stops))
		if route.Driver != nil && haversineKm(route.Driver.GetCoords(), models.Coordinates{Lat: lat / n, Lng: lng / n}) > 15 {
			m.FarDrivers++
		}
		if backtracks(radii, result.Mode == models.RouteModePickup) {
			m.BacktrackingCars++
		}
	}
	if m.CarsUsed > 0 {
		m.AverageDetourMin /= float64(m.CarsUsed)
	}
	for _, cars := range homes {
		if len(cars) > 1 {
			m.SplitHouseholds++
		}
	}
	m.TotalDistanceKm = math.Round(m.TotalDistanceKm*10) / 10
	m.MaxDetourMin = math.Round(m.MaxDetourMin*10) / 10
	m.AverageDetourMin = math.Round(m.AverageDetourMin*10) / 10
	m.LongestRiderMin = math.Round(m.LongestRiderMin*10) / 10
	return m
}

// backtracks reports whether the stop sequence gives back more than 2 km of
// progress: in dropoff a later stop should not be much nearer the venue than
// an earlier one; in pickup the mirror.
func backtracks(radii []float64, pickup bool) bool {
	for i := range radii {
		for j := i + 1; j < len(radii); j++ {
			if pickup && radii[j] > radii[i]+2 {
				return true
			}
			if !pickup && radii[j] < radii[i]-2 {
				return true
			}
		}
	}
	return false
}
