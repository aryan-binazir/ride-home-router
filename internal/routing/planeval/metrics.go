package planeval

import (
	"math"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"sort"
)

type Metrics struct {
	CarsUsed         int     `json:"cars_used"`
	TotalDistanceKm  float64 `json:"total_distance_km"`
	MaxDetourMin     float64 `json:"max_detour_min"`
	AverageDetourMin float64 `json:"average_detour_min"`
	LongestRiderMin  float64 `json:"longest_rider_min"`
	FarDrivers       int     `json:"far_drivers"`
	BacktrackingCars int     `json:"backtracking_cars"`
	SplitHouseholds  int     `json:"split_households"`
	BurdenP95Min     float64 `json:"burden_p95_min"`
	BurdenMaxMin     float64 `json:"burden_max_min"`
	HomePassCars     int     `json:"home_pass_cars"`
	TimedOut         bool    `json:"timed_out,omitempty"`
	SolveMs          int64   `json:"-"`
}

const (
	farDriverKm         = 15
	backtrackKm         = 2
	homePassProximityKm = 2
	homePassRiderKm     = 5
	burdenPercentile    = 0.95
)

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
			burdens = append(burdens, detourMinutesPerRider(route.DetourSecs, riders))
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
		if route.Driver != nil && haversineKm(route.Driver.GetCoords(), models.Coordinates{Lat: lat / n, Lng: lng / n}) > farDriverKm {
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
		m.BurdenP95Min = nearestRank(burdens, burdenPercentile)
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

func detourMinutesPerRider(detourSecs float64, riders int) float64 {
	return detourSecs / 60 / float64(riders)
}

func nearestRank(sorted []float64, percentile float64) float64 {
	return sorted[int(math.Ceil(percentile*float64(len(sorted))))-1]
}

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
		if segmentDistanceKm(home, points[i], points[i+1]) > homePassProximityKm {
			continue
		}
		for _, later := range points[i+1:] {
			if haversineKm(home, later) > homePassRiderKm {
				return true
			}
		}
	}
	return false
}

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

func backtracks(radii []float64, pickup bool) bool {
	for i := range radii {
		for j := i + 1; j < len(radii); j++ {
			if pickup && radii[j] > radii[i]+backtrackKm {
				return true
			}
			if !pickup && radii[j] < radii[i]-backtrackKm {
				return true
			}
		}
	}
	return false
}
