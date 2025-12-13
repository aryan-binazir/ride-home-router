package testutil

import (
	"context"
	"fmt"
	"math"

	"ride-home-router/internal/models"
)

// DistanceResult is the result type for MockDistanceCalculator
type DistanceResult struct {
	DistanceMeters float64
	DurationSecs   float64
}

// DistanceCall tracks a call to the distance calculator
type DistanceCall struct {
	Origin models.Coordinates
	Dest   models.Coordinates
}

// MockDistanceCalculator is a mock implementation for testing.
// It calculates Euclidean distance (scaled) between coordinates for deterministic tests.
type MockDistanceCalculator struct {
	ScaleFactor float64
	Overrides   map[string]*DistanceResult
	Calls       []DistanceCall
}

func NewMockDistanceCalculator() *MockDistanceCalculator {
	return &MockDistanceCalculator{
		ScaleFactor: 111000, // 1 degree ≈ 111km in meters
		Overrides:   make(map[string]*DistanceResult),
		Calls:       []DistanceCall{},
	}
}

func (m *MockDistanceCalculator) makeKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
}

// SetDistance sets a custom distance for a specific origin-destination pair
func (m *MockDistanceCalculator) SetDistance(origin, dest models.Coordinates, distMeters, durSecs float64) {
	key := m.makeKey(origin, dest)
	m.Overrides[key] = &DistanceResult{
		DistanceMeters: distMeters,
		DurationSecs:   durSecs,
	}
}

// euclideanDistance calculates scaled Euclidean distance between two coordinates
func (m *MockDistanceCalculator) euclideanDistance(origin, dest models.Coordinates) float64 {
	dLat := dest.Lat - origin.Lat
	dLng := dest.Lng - origin.Lng
	return math.Sqrt(dLat*dLat+dLng*dLng) * m.ScaleFactor
}

// GetDistance returns the distance between two points
func (m *MockDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error) {
	m.Calls = append(m.Calls, DistanceCall{Origin: origin, Dest: dest})

	// Check for override
	key := m.makeKey(origin, dest)
	if override, ok := m.Overrides[key]; ok {
		return override, nil
	}

	// Same point = 0 distance
	if models.RoundCoordinate(origin.Lat) == models.RoundCoordinate(dest.Lat) &&
		models.RoundCoordinate(origin.Lng) == models.RoundCoordinate(dest.Lng) {
		return &DistanceResult{DistanceMeters: 0, DurationSecs: 0}, nil
	}

	// Calculate Euclidean distance
	dist := m.euclideanDistance(origin, dest)
	// Assume average speed of 50 km/h for duration
	dur := dist / 50000 * 3600

	return &DistanceResult{
		DistanceMeters: dist,
		DurationSecs:   dur,
	}, nil
}

// GetDistanceMatrix returns a matrix of distances between all pairs of points
func (m *MockDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error) {
	n := len(points)
	if n == 0 {
		return [][]DistanceResult{}, nil
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
		for j := range matrix[i] {
			if i == j {
				matrix[i][j] = DistanceResult{DistanceMeters: 0, DurationSecs: 0}
			} else {
				result, _ := m.GetDistance(ctx, points[i], points[j])
				matrix[i][j] = *result
			}
		}
	}

	return matrix, nil
}

// GetDistancesFromPoint returns distances from a single origin to multiple destinations
func (m *MockDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error) {
	results := make([]DistanceResult, len(destinations))
	for i, dest := range destinations {
		result, _ := m.GetDistance(ctx, origin, dest)
		results[i] = *result
	}
	return results, nil
}

// PrewarmCache is a no-op for the mock
func (m *MockDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

// ResetCalls clears the recorded calls
func (m *MockDistanceCalculator) ResetCalls() {
	m.Calls = []DistanceCall{}
}

// MockDistanceCache is a mock implementation of DistanceCacheRepository for testing
type MockDistanceCache struct {
	entries map[string]*models.DistanceCacheEntry
}

func NewMockDistanceCache() *MockDistanceCache {
	return &MockDistanceCache{
		entries: make(map[string]*models.DistanceCacheEntry),
	}
}

func (c *MockDistanceCache) cacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat), models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat), models.RoundCoordinate(dest.Lng))
}

func (c *MockDistanceCache) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	key := c.cacheKey(origin, dest)
	if entry, ok := c.entries[key]; ok {
		return entry, nil
	}
	return nil, nil
}

func (c *MockDistanceCache) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	result := make(map[string]*models.DistanceCacheEntry)
	for _, pair := range pairs {
		entry, _ := c.Get(ctx, pair.Origin, pair.Dest)
		if entry != nil {
			key := c.cacheKey(pair.Origin, pair.Dest)
			result[key] = entry
		}
	}
	return result, nil
}

func (c *MockDistanceCache) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	key := c.cacheKey(entry.Origin, entry.Destination)
	c.entries[key] = entry
	return nil
}

func (c *MockDistanceCache) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	for i := range entries {
		c.Set(ctx, &entries[i])
	}
	return nil
}

func (c *MockDistanceCache) Clear(ctx context.Context) error {
	c.entries = make(map[string]*models.DistanceCacheEntry)
	return nil
}

// Count returns the number of entries in the cache
func (c *MockDistanceCache) Count() int {
	return len(c.entries)
}
