package testsupport

import (
	"context"
	"fmt"
	"math"
	"strings"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
)

type e2eGeocoder struct{}

type e2eDistanceCalculator struct{}

type e2eLocation struct {
	displayName string
	formatted   string
	coords      models.Coordinates
}

var e2eAddressIndex = map[string]e2eLocation{
	"100 market st, san francisco, ca 94105": {
		displayName: "100 Market St, San Francisco, California 94105, United States",
		formatted:   "100 Market St, San Francisco, CA 94105",
		coords:      models.Coordinates{Lat: 37.793600, Lng: -122.395000},
	},
	"500 howard st, san francisco, ca 94105": {
		displayName: "500 Howard St, San Francisco, California 94105, United States",
		formatted:   "500 Howard St, San Francisco, CA 94105",
		coords:      models.Coordinates{Lat: 37.788000, Lng: -122.396400},
	},
	"1 dr carlton b goodlett pl, san francisco, ca 94102": {
		displayName: "1 Dr Carlton B Goodlett Pl, San Francisco, California 94102, United States",
		formatted:   "1 Dr Carlton B Goodlett Pl, San Francisco, CA 94102",
		coords:      models.Coordinates{Lat: 37.779300, Lng: -122.419200},
	},
}

// NewE2EGeocoder returns a deterministic geocoder for E2E tests.
func NewE2EGeocoder() geocoding.Geocoder {
	return &e2eGeocoder{}
}

func (g *e2eGeocoder) Geocode(ctx context.Context, address string) (*geocoding.GeocodingResult, error) {
	_ = ctx
	normalized := strings.ToLower(strings.TrimSpace(address))
	location, ok := e2eAddressIndex[normalized]
	if !ok {
		lat, lng := syntheticCoords(normalized)
		return &geocoding.GeocodingResult{
			Coords:           models.Coordinates{Lat: lat, Lng: lng},
			DisplayName:      strings.TrimSpace(address),
			FormattedAddress: strings.TrimSpace(address),
		}, nil
	}

	return &geocoding.GeocodingResult{
		Coords:           location.coords,
		DisplayName:      location.displayName,
		FormattedAddress: location.formatted,
	}, nil
}

func (g *e2eGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*geocoding.GeocodingResult, error) {
	_ = maxRetries
	return g.Geocode(ctx, address)
}

func (g *e2eGeocoder) Search(ctx context.Context, query string, limit int) ([]geocoding.GeocodingResult, error) {
	_ = ctx
	q := strings.ToLower(strings.TrimSpace(query))
	results := make([]geocoding.GeocodingResult, 0, len(e2eAddressIndex))
	for key, location := range e2eAddressIndex {
		if strings.Contains(key, q) || strings.Contains(strings.ToLower(location.formatted), q) {
			results = append(results, geocoding.GeocodingResult{
				Coords:           location.coords,
				DisplayName:      location.displayName,
				FormattedAddress: location.formatted,
			})
		}
	}

	if len(results) == 0 && q != "" {
		lat, lng := syntheticCoords(q)
		results = append(results, geocoding.GeocodingResult{
			Coords:           models.Coordinates{Lat: lat, Lng: lng},
			DisplayName:      strings.TrimSpace(query),
			FormattedAddress: strings.TrimSpace(query),
		})
	}

	if limit > 0 && len(results) > limit {
		return results[:limit], nil
	}
	return results, nil
}

// NewE2EDistanceCalculator returns a deterministic distance calculator for E2E tests.
func NewE2EDistanceCalculator() distance.DistanceCalculator {
	return &e2eDistanceCalculator{}
}

func (c *e2eDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	_ = ctx
	if models.RoundCoordinate(origin.Lat) == models.RoundCoordinate(dest.Lat) &&
		models.RoundCoordinate(origin.Lng) == models.RoundCoordinate(dest.Lng) {
		return &distance.DistanceResult{}, nil
	}

	meters := haversineMeters(origin, dest)
	durationSecs := meters / 11.176 // ~25 mph / 40.2 kmh urban average
	return &distance.DistanceResult{
		DistanceMeters: meters,
		DurationSecs:   durationSecs,
	}, nil
}

func (c *e2eDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	matrix := make([][]distance.DistanceResult, len(points))
	for i := range points {
		matrix[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			dist, err := c.GetDistance(ctx, points[i], points[j])
			if err != nil {
				return nil, err
			}
			matrix[i][j] = *dist
		}
	}
	return matrix, nil
}

func (c *e2eDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i := range destinations {
		dist, err := c.GetDistance(ctx, origin, destinations[i])
		if err != nil {
			return nil, err
		}
		results[i] = *dist
	}
	return results, nil
}

func (c *e2eDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	_ = ctx
	_ = points
	return nil
}

func syntheticCoords(seed string) (float64, float64) {
	var hash uint32 = 2166136261
	for _, b := range []byte(seed) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	latOffset := float64(hash%1200)/10000 - 0.06
	lngOffset := float64((hash/1200)%1200)/10000 - 0.06
	return 37.7800 + latOffset, -122.4100 + lngOffset
}

func haversineMeters(a, b models.Coordinates) float64 {
	const earthRadiusMeters = 6371000.0
	lat1 := toRadians(a.Lat)
	lat2 := toRadians(b.Lat)
	dLat := lat2 - lat1
	dLon := toRadians(b.Lng - a.Lng)
	sinDLat := math.Sin(dLat / 2)
	sinDLon := math.Sin(dLon / 2)
	h := sinDLat*sinDLat + math.Cos(lat1)*math.Cos(lat2)*sinDLon*sinDLon
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(h))
}

func toRadians(degrees float64) float64 {
	return degrees * math.Pi / 180
}

func init() {
	// Ensure map keys stay normalized even if edited in future.
	for key := range e2eAddressIndex {
		if key != strings.ToLower(strings.TrimSpace(key)) {
			panic(fmt.Sprintf("e2e address key must be normalized: %q", key))
		}
	}
}
