package distance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ride-home-router/internal/models"
	"ride-home-router/internal/testutil"
)

func TestGetDistanceMatrix_AllCached(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	// Pre-populate cache with all pairs
	points := []models.Coordinates{
		{Lat: 0, Lng: 0},
		{Lat: 0.1, Lng: 0},
		{Lat: 0, Lng: 0.1},
	}

	for i, p1 := range points {
		for j, p2 := range points {
			if i != j {
				cache.Set(context.Background(), &models.DistanceCacheEntry{
					Origin:         p1,
					Destination:    p2,
					DistanceMeters: float64((i+1)*1000 + j*100),
					DurationSecs:   float64((i+1)*60 + j*10),
				})
			}
		}
	}

	// Create server that should NOT be called
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		t.Error("OSRM server should not be called when all data is cached")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: server.Client(),
		cache:      cache,
	}

	matrix, err := calc.GetDistanceMatrix(context.Background(), points)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if serverCalled {
		t.Error("server was called when all data should be cached")
	}

	// Verify matrix has correct values from cache
	if len(matrix) != 3 {
		t.Fatalf("expected 3x3 matrix, got %dx%d", len(matrix), len(matrix))
	}

	// Check diagonal is zero
	for i := 0; i < 3; i++ {
		if matrix[i][i].DistanceMeters != 0 {
			t.Errorf("diagonal [%d][%d] should be 0, got %f", i, i, matrix[i][i].DistanceMeters)
		}
	}

	// Check a specific cached value
	if matrix[0][1].DistanceMeters != 1100 { // (0+1)*1000 + 1*100
		t.Errorf("expected matrix[0][1] = 1100, got %f", matrix[0][1].DistanceMeters)
	}
}

func TestGetDistanceMatrix_PartialCache(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	points := []models.Coordinates{
		{Lat: 0, Lng: 0},
		{Lat: 0.1, Lng: 0},
	}

	// Only cache one direction
	cache.Set(context.Background(), &models.DistanceCacheEntry{
		Origin:         points[0],
		Destination:    points[1],
		DistanceMeters: 5000,
		DurationSecs:   300,
	})

	apiCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalled = true
		resp := osrmTableResponse{
			Code: "Ok",
			Distances: [][]float64{
				{0, 11100},
				{11100, 0},
			},
			Durations: [][]float64{
				{0, 600},
				{600, 0},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: server.Client(),
		cache:      cache,
	}

	matrix, err := calc.GetDistanceMatrix(context.Background(), points)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// API should be called because there were missing pairs
	if !apiCalled {
		t.Error("expected API to be called for missing pairs")
	}

	// Matrix should have values (from API)
	if matrix[0][1].DistanceMeters == 0 {
		t.Error("expected matrix[0][1] to have distance")
	}
	if matrix[1][0].DistanceMeters == 0 {
		t.Error("expected matrix[1][0] to have distance")
	}

	// Verify cache was updated with entries from API
	if cache.Count() < 1 {
		t.Errorf("expected cache to be populated after API call, got %d entries", cache.Count())
	}
}

func TestGetDistanceMatrix_BatchSplitting(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	// Create more points than maxOSRMCoordinates (80)
	numPoints := 85
	points := make([]models.Coordinates, numPoints)
	for i := 0; i < numPoints; i++ {
		points[i] = models.Coordinates{
			Lat: float64(i) * 0.01,
			Lng: float64(i) * 0.01,
		}
	}

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		// Parse the coordinates to determine response size
		path := r.URL.Path
		coordStr := strings.TrimPrefix(path, "/table/v1/driving/")
		coords := strings.Split(coordStr, ";")
		n := len(coords)

		// Build response matrix of appropriate size
		distances := make([][]float64, n)
		durations := make([][]float64, n)
		for i := 0; i < n; i++ {
			distances[i] = make([]float64, n)
			durations[i] = make([]float64, n)
			for j := 0; j < n; j++ {
				if i != j {
					distances[i][j] = float64((i+j)*100 + 1000)
					durations[i][j] = float64((i + j) * 10)
				}
			}
		}

		// Check for sources/destinations parameters (batched request)
		sources := r.URL.Query().Get("sources")
		dests := r.URL.Query().Get("destinations")
		if sources != "" && dests != "" {
			// Batched request - adjust matrix size
			srcIndices := strings.Split(sources, ";")
			destIndices := strings.Split(dests, ";")

			batchedDist := make([][]float64, len(srcIndices))
			batchedDur := make([][]float64, len(srcIndices))
			for i := range srcIndices {
				batchedDist[i] = make([]float64, len(destIndices))
				batchedDur[i] = make([]float64, len(destIndices))
				for j := range destIndices {
					batchedDist[i][j] = float64((i+j)*100 + 1000)
					batchedDur[i][j] = float64((i + j) * 10)
				}
			}
			distances = batchedDist
			durations = batchedDur
		}

		resp := osrmTableResponse{
			Code:      "Ok",
			Distances: distances,
			Durations: durations,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: server.Client(),
		cache:      cache,
	}

	matrix, err := calc.GetDistanceMatrix(context.Background(), points)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify matrix dimensions
	if len(matrix) != numPoints {
		t.Fatalf("expected %dx%d matrix, got %dx%d", numPoints, numPoints, len(matrix), len(matrix))
	}

	// With 85 points and max 80 per request, should need multiple batches
	// The batching creates batches of indices and makes requests for batch pairs
	if requestCount < 2 {
		t.Errorf("expected multiple requests for %d points (max %d per request), got %d requests",
			numPoints, maxOSRMCoordinates, requestCount)
	}

	// Verify diagonal is zero
	for i := 0; i < numPoints; i++ {
		if matrix[i][i].DistanceMeters != 0 {
			t.Errorf("diagonal [%d][%d] should be 0, got %f", i, i, matrix[i][i].DistanceMeters)
		}
	}
}

func TestCoordinateRounding_Consistency(t *testing.T) {
	// Test that coordinate rounding is consistent across the codebase
	testCases := []struct {
		input    float64
		expected float64
	}{
		{0.123456789, 0.12346}, // rounds up
		{0.123454, 0.12345},    // rounds down
		{0.123455, 0.12346},    // rounds up (0.5)
		{-0.123456, -0.12346},  // negative
		{0.0, 0.0},             // zero
		{1.0, 1.0},             // whole number
		{0.000001, 0.0},        // very small (rounds to 0)
		{0.000009, 0.00001},    // small but significant
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			result := models.RoundCoordinate(tc.input)
			if result != tc.expected {
				t.Errorf("RoundCoordinate(%v) = %v, expected %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestGetDistance_SamePoint(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for same point")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: server.Client(),
		cache:      cache,
	}

	// Points that round to the same value
	origin := models.Coordinates{Lat: 0.123456, Lng: 0.654321}
	dest := models.Coordinates{Lat: 0.123456, Lng: 0.654321}

	result, err := calc.GetDistance(context.Background(), origin, dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DistanceMeters != 0 {
		t.Errorf("expected 0 distance for same point, got %f", result.DistanceMeters)
	}
	if result.DurationSecs != 0 {
		t.Errorf("expected 0 duration for same point, got %f", result.DurationSecs)
	}
}

func TestGetDistanceMatrix_Empty(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	calc := &osrmCalculator{
		baseURL:    "http://not-called",
		httpClient: http.DefaultClient,
		cache:      cache,
	}

	matrix, err := calc.GetDistanceMatrix(context.Background(), []models.Coordinates{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(matrix) != 0 {
		t.Errorf("expected empty matrix, got %d elements", len(matrix))
	}
}

func TestGetDistanceMatrix_SinglePoint(t *testing.T) {
	cache := testutil.NewMockDistanceCache()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := osrmTableResponse{
			Code:      "Ok",
			Distances: [][]float64{{0}},
			Durations: [][]float64{{0}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	calc := &osrmCalculator{
		baseURL:    server.URL,
		httpClient: server.Client(),
		cache:      cache,
	}

	points := []models.Coordinates{{Lat: 0.1, Lng: 0.1}}
	matrix, err := calc.GetDistanceMatrix(context.Background(), points)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(matrix) != 1 || len(matrix[0]) != 1 {
		t.Fatalf("expected 1x1 matrix, got %dx%d", len(matrix), len(matrix[0]))
	}

	if matrix[0][0].DistanceMeters != 0 {
		t.Errorf("expected 0 distance for single point, got %f", matrix[0][0].DistanceMeters)
	}
}
