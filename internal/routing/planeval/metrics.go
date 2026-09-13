package planeval

import (
	"math"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"sort"
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
	// BurdenP95Min and BurdenMaxMin are driver detour minutes per rider served,
	// the 95th percentile (nearest rank) and the worst car: a long excursion for
	// two riders reads worse than a full van's.
	BurdenP95Min float64 `json:"burden_p95_min"`
	BurdenMaxMin float64 `json:"burden_max_min"`
	// HomePassCars counts cars whose path comes within 2 km of the driver's
	// home and then still has a rider more than 5 km from that home to serve.
	HomePassCars int   `json:"home_pass_cars"`
	TimedOut     bool  `json:"timed_out,omitempty"` // no plan within the production calculation timeout
	SolveMs      int64 `json:"-"`                   // reported, never part of the committed baseline
}

// Measure summarises a plan. Distances and times are the planner's own
// estimates, so they compare like with like across planner versions.
func Measure(result *models.RoutingResult, venue models.Coordinates, solveMs int64) Metrics {
	m := Metrics{SolveMs: solveMs}
	homes := map[string]map[int64]struct{}{}
	var burdens []float64
	for i, route := range result.Routes {
		if len(route.Stops) == 0 {
			continue
		}
		m.CarsUsed++
		m.TotalDistanceKm += route.TotalDistanceMeters / 1000
		m.MaxDetourMin = math.Max(m.MaxDetourMin, route.DetourSecs/60)
		m.AverageDetourMin += route.DetourSecs / 60
		riders := 0
		for _, stop := range route.Stops {
			if stop.Participant != nil {
				riders++
			}
		}
		if riders > 0 {
			burdens = append(burdens, route.DetourSecs/60/float64(riders))
		}
		var lat, lng float64
		var radii []float64
		var path []models.Coordinates
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
			path = append(path, stop.Participant.GetCoords())
			key := routing.HouseholdKey(stop.Participant)
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
		if route.Driver != nil && passesHome(venue, route.Driver.GetCoords(), path, result.Mode == models.RouteModePickup) {
			m.HomePassCars++
		}
	}
	if len(burdens) > 0 {
		sort.Float64s(burdens)
		m.BurdenP95Min = burdens[int(math.Ceil(0.95*float64(len(burdens))))-1]
		m.BurdenMaxMin = burdens[len(burdens)-1]
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

// passesHome reports whether the car's straight-line path (venue → riders, in
// venue-to-riders order) comes within 2 km of the driver's home on some leg
// while a rider more than 5 km from that home is still to be served, that
// leg's destination included. The final leg to the driver's home is not a
// leg here. Pickup routes are reversed into venue-to-riders order first.
func passesHome(venue, home models.Coordinates, riders []models.Coordinates, pickup bool) bool {
	ordered := riders
	if pickup {
		ordered = make([]models.Coordinates, len(riders))
		for i, c := range riders {
			ordered[len(riders)-1-i] = c
		}
	}
	points := append([]models.Coordinates{venue}, ordered...)
	for i := 0; i+1 < len(points); i++ {
		if segmentDistanceKm(home, points[i], points[i+1]) > 2 {
			continue
		}
		for _, later := range points[i+1:] {
			if haversineKm(home, later) > 5 {
				return true
			}
		}
	}
	return false
}

// segmentDistanceKm measures distance to the shortest great-circle segment.
func segmentDistanceKm(p, a, b models.Coordinates) float64 {
	const radiusKm = 6371.0088
	length := haversineKm(a, b) / radiusKm
	if length < 1e-12 {
		return haversineKm(p, a)
	}
	bearing := func(from, to models.Coordinates) float64 {
		lat1, lat2, delta := from.Lat*math.Pi/180, to.Lat*math.Pi/180, (to.Lng-from.Lng)*math.Pi/180
		return math.Atan2(math.Sin(delta)*math.Cos(lat2), math.Cos(lat1)*math.Sin(lat2)-math.Sin(lat1)*math.Cos(lat2)*math.Cos(delta))
	}
	distance := haversineKm(a, p) / radiusKm
	angle := bearing(a, p) - bearing(a, b)
	along := math.Atan2(math.Sin(distance)*math.Cos(angle), math.Cos(distance))
	if along < 0 || along > length {
		return math.Min(haversineKm(p, a), haversineKm(p, b))
	}
	cross := math.Sin(distance) * math.Sin(angle)
	return radiusKm * math.Abs(math.Asin(math.Max(-1, math.Min(1, cross))))
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
