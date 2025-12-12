package geocoding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNominatimGeocodeSuccess(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Contains(t, r.URL.Path, "/search")
		assert.Equal(t, "json", r.URL.Query().Get("format"))
		assert.Equal(t, "1", r.URL.Query().Get("limit"))
		assert.NotEmpty(t, r.URL.Query().Get("q"))

		// Return mock response
		response := []nominatimResponse{
			{
				Lat:         "40.7128",
				Lon:         "-74.0060",
				DisplayName: "New York, NY, USA",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create geocoder with mock server URL
	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond), // Fast rate limit for testing
	}

	ctx := context.Background()
	result, err := geocoder.Geocode(ctx, "New York")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 40.7128, result.Coords.Lat)
	assert.Equal(t, -74.0060, result.Coords.Lng)
	assert.Equal(t, "New York, NY, USA", result.DisplayName)
}

func TestNominatimGeocodeNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty results
		response := []nominatimResponse{}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.Geocode(ctx, "Nonexistent Location")

	require.Error(t, err)
	assert.Nil(t, result)

	geocodingErr, ok := err.(*ErrGeocodingFailed)
	require.True(t, ok)
	assert.Contains(t, geocodingErr.Reason, "no results found")
}

func TestNominatimGeocodeHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.Geocode(ctx, "Test Address")

	require.Error(t, err)
	assert.Nil(t, result)

	geocodingErr, ok := err.(*ErrGeocodingFailed)
	require.True(t, ok)
	assert.Contains(t, geocodingErr.Reason, "HTTP 500")
}

func TestNominatimGeocodeInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.Geocode(ctx, "Test Address")

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestNominatimGeocodeInvalidLatLon(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := []nominatimResponse{
			{
				Lat:         "invalid",
				Lon:         "-74.0060",
				DisplayName: "Test",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.Geocode(ctx, "Test Address")

	require.Error(t, err)
	assert.Nil(t, result)

	geocodingErr, ok := err.(*ErrGeocodingFailed)
	require.True(t, ok)
	assert.Contains(t, geocodingErr.Reason, "invalid latitude")
}

func TestNominatimGeocodeRateLimiting(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		response := []nominatimResponse{
			{
				Lat:         "40.7128",
				Lon:         "-74.0060",
				DisplayName: "Test",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(50 * time.Millisecond),
	}

	ctx := context.Background()

	// Make multiple requests
	start := time.Now()
	for i := 0; i < 3; i++ {
		_, err := geocoder.Geocode(ctx, "Test")
		require.NoError(t, err)
	}
	elapsed := time.Since(start)

	// Should take at least 100ms for 3 requests (50ms * 2 waits)
	assert.True(t, elapsed >= 100*time.Millisecond, "Rate limiting not working")
	assert.Equal(t, 3, requestCount)
}

func TestNominatimGeocodeWithRetrySuccess(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 2 {
			// Fail first attempt
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Succeed on second attempt
		response := []nominatimResponse{
			{
				Lat:         "40.7128",
				Lon:         "-74.0060",
				DisplayName: "New York",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.GeocodeWithRetry(ctx, "New York", 3)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 40.7128, result.Coords.Lat)
	assert.Equal(t, 2, attemptCount)
}

func TestNominatimGeocodeWithRetryAllFail(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	result, err := geocoder.GeocodeWithRetry(ctx, "Test", 3)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 3, attemptCount)
}

func TestNominatimGeocodeContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(100 * time.Millisecond)
		response := []nominatimResponse{
			{Lat: "40.7128", Lon: "-74.0060", DisplayName: "Test"},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := geocoder.Geocode(ctx, "Test")

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestNominatimGeocodeUserAgent(t *testing.T) {
	userAgentReceived := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgentReceived = r.Header.Get("User-Agent")
		response := []nominatimResponse{
			{Lat: "40.7128", Lon: "-74.0060", DisplayName: "Test"},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	geocoder := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}

	ctx := context.Background()
	_, err := geocoder.Geocode(ctx, "Test")

	require.NoError(t, err)
	assert.Equal(t, "RideHomeRouter/1.0", userAgentReceived)
}
