package distance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

// Mock distance cache for testing
type mockDistanceCache struct {
	cache map[string]*models.DistanceCacheEntry
}

func newMockDistanceCache() *mockDistanceCache {
	return &mockDistanceCache{
		cache: make(map[string]*models.DistanceCacheEntry),
	}
}

func makeCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
}

func (m *mockDistanceCache) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	key := makeCacheKey(origin, dest)
	if entry, ok := m.cache[key]; ok {
		return entry, nil
	}
	return nil, nil
}

func (m *mockDistanceCache) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	result := make(map[string]*models.DistanceCacheEntry)
	for _, pair := range pairs {
		entry, _ := m.Get(ctx, pair.Origin, pair.Dest)
		if entry != nil {
			key := makeCacheKey(pair.Origin, pair.Dest)
			result[key] = entry
		}
	}
	return result, nil
}

func (m *mockDistanceCache) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	key := makeCacheKey(entry.Origin, entry.Destination)
	m.cache[key] = entry
	return nil
}

func (m *mockDistanceCache) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	for _, entry := range entries {
		m.Set(ctx, &entry)
	}
	return nil
}

func (m *mockDistanceCache) Clear(ctx context.Context) error {
	m.cache = make(map[string]*models.DistanceCacheEntry)
	return nil
}

var _ database.DistanceCacheRepository = (*mockDistanceCache)(nil)

func TestOSRMGetDistanceSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "/table/v1/driving/")

		response := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 35000},
				{35000, 0},
			},
			Durations: [][]float64{
				{0, 3600},
				{3600, 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	origin := models.Coordinates{Lat: 40.7128, Lng: -74.0060}
	dest := models.Coordinates{Lat: 42.3601, Lng: -71.0589}

	ctx := context.Background()
	result, err := calc.GetDistance(ctx, origin, dest)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 35000.0, result.DistanceMeters)
	assert.Equal(t, 3600.0, result.DurationSecs)
}

func TestOSRMGetDistanceFromCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Should not call OSRM API when cache hit")
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	origin := models.Coordinates{Lat: 40.0, Lng: -75.0}
	dest := models.Coordinates{Lat: 41.0, Lng: -76.0}

	// Pre-populate cache
	cache.Set(context.Background(), &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 12345.0,
		DurationSecs:   1234.0,
	})

	ctx := context.Background()
	result, err := calc.GetDistance(ctx, origin, dest)

	require.NoError(t, err)
	assert.Equal(t, 12345.0, result.DistanceMeters)
	assert.Equal(t, 1234.0, result.DurationSecs)
}

func TestOSRMGetDistanceMatrixSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 10000, 20000},
				{10000, 0, 15000},
				{20000, 15000, 0},
			},
			Durations: [][]float64{
				{0, 1000, 2000},
				{1000, 0, 1500},
				{2000, 1500, 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
		{Lat: 42.0, Lng: -77.0},
	}

	ctx := context.Background()
	matrix, err := calc.GetDistanceMatrix(ctx, points)

	require.NoError(t, err)
	assert.Len(t, matrix, 3)
	assert.Len(t, matrix[0], 3)

	// Diagonal should be zero
	assert.Equal(t, 0.0, matrix[0][0].DistanceMeters)
	assert.Equal(t, 0.0, matrix[1][1].DistanceMeters)
	assert.Equal(t, 0.0, matrix[2][2].DistanceMeters)

	// Check values
	assert.Equal(t, 10000.0, matrix[0][1].DistanceMeters)
	assert.Equal(t, 20000.0, matrix[0][2].DistanceMeters)
	assert.Equal(t, 15000.0, matrix[1][2].DistanceMeters)

	// Verify cache was populated
	cached, _ := cache.Get(ctx, points[0], points[1])
	assert.NotNil(t, cached)
	assert.Equal(t, 10000.0, cached.DistanceMeters)
}

func TestOSRMGetDistanceMatrixPartialCache(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		response := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 10000},
				{10000, 0},
			},
			Durations: [][]float64{
				{0, 1000},
				{1000, 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
	}

	ctx := context.Background()

	// First call - should hit API
	matrix1, err := calc.GetDistanceMatrix(ctx, points)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)
	assert.Equal(t, 10000.0, matrix1[0][1].DistanceMeters)

	// Second call - should use cache
	matrix2, err := calc.GetDistanceMatrix(ctx, points)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount) // Should not increment
	assert.Equal(t, 10000.0, matrix2[0][1].DistanceMeters)
}

func TestOSRMGetDistanceMatrixEmpty(t *testing.T) {
	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    "http://localhost",
		httpClient: &http.Client{},
		cache:      cache,
	}

	ctx := context.Background()
	matrix, err := calc.GetDistanceMatrix(ctx, []models.Coordinates{})

	require.NoError(t, err)
	assert.Empty(t, matrix)
}

func TestOSRMGetDistancesFromPoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 10000, 15000},
				{10000, 0, 12000},
				{15000, 12000, 0},
			},
			Durations: [][]float64{
				{0, 1000, 1500},
				{1000, 0, 1200},
				{1500, 1200, 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	origin := models.Coordinates{Lat: 40.0, Lng: -75.0}
	destinations := []models.Coordinates{
		{Lat: 41.0, Lng: -76.0},
		{Lat: 42.0, Lng: -77.0},
	}

	ctx := context.Background()
	results, err := calc.GetDistancesFromPoint(ctx, origin, destinations)

	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Equal(t, 10000.0, results[0].DistanceMeters)
	assert.Equal(t, 15000.0, results[1].DistanceMeters)
}

func TestOSRMGetDistancesFromPointEmpty(t *testing.T) {
	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    "http://localhost",
		httpClient: &http.Client{},
		cache:      cache,
	}

	origin := models.Coordinates{Lat: 40.0, Lng: -75.0}

	ctx := context.Background()
	results, err := calc.GetDistancesFromPoint(ctx, origin, []models.Coordinates{})

	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestOSRMPrewarmCache(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 10000},
				{10000, 0},
			},
			Durations: [][]float64{
				{0, 1000},
				{1000, 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
	}

	ctx := context.Background()
	err := calc.PrewarmCache(ctx, points)

	require.NoError(t, err)

	// Verify cache was populated
	cached, _ := cache.Get(ctx, points[0], points[1])
	assert.NotNil(t, cached)
	assert.Equal(t, 10000.0, cached.DistanceMeters)
}

func TestOSRMHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Server Error"))
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
	}

	ctx := context.Background()
	_, err := calc.GetDistanceMatrix(ctx, points)

	require.Error(t, err)
	distErr, ok := err.(*ErrDistanceCalculationFailed)
	require.True(t, ok)
	assert.Contains(t, distErr.Reason, "HTTP 500")
}

func TestOSRMInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
	}

	ctx := context.Background()
	_, err := calc.GetDistanceMatrix(ctx, points)

	require.Error(t, err)
}

func TestOSRMErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := osrmTableResponse{
			Code: "NoRoute",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	cache := newMockDistanceCache()
	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: &http.Client{},
		cache:      cache,
	}

	points := []models.Coordinates{
		{Lat: 40.0, Lng: -75.0},
		{Lat: 41.0, Lng: -76.0},
	}

	ctx := context.Background()
	_, err := calc.GetDistanceMatrix(ctx, points)

	require.Error(t, err)
	distErr, ok := err.(*ErrDistanceCalculationFailed)
	require.True(t, ok)
	assert.Contains(t, distErr.Reason, "NoRoute")
}
