package distance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type googleTestCache struct {
	mu      sync.Mutex
	entries map[string]*models.DistanceCacheEntry
}

type googleRoundTripFunc func(*http.Request) (*http.Response, error)

func (f googleRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newGoogleTestCache() *googleTestCache {
	return &googleTestCache{entries: make(map[string]*models.DistanceCacheEntry)}
}

func (c *googleTestCache) Get(_ context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[PairCacheKey(origin, dest)]
	if entry == nil {
		return nil, database.ErrCacheMiss
	}
	return entry, nil
}

func (c *googleTestCache) GetBatch(_ context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make(map[string]*models.DistanceCacheEntry)
	for _, pair := range pairs {
		key := PairCacheKey(pair.Origin, pair.Dest)
		if entry := c.entries[key]; entry != nil {
			result[key] = entry
		}
	}
	return result, nil
}

func (c *googleTestCache) Set(_ context.Context, entry *models.DistanceCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[PairCacheKey(entry.Origin, entry.Destination)] = entry
	return nil
}

func (c *googleTestCache) SetBatch(_ context.Context, entries []models.DistanceCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range entries {
		entry := entries[i]
		c.entries[PairCacheKey(entry.Origin, entry.Destination)] = &entry
	}
	return nil
}

func (c *googleTestCache) Clear(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*models.DistanceCacheEntry)
	return nil
}

func newTestGoogleCalculator(t *testing.T, handler http.HandlerFunc) (*googleCalculator, *googleTestCache) {
	t.Helper()

	cache := newGoogleTestCache()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	calc := NewGoogleCalculator(cache, func() (string, error) {
		return "test-api-key", nil
	}).(*googleCalculator)
	calc.endpoint = server.URL
	calc.httpClient = server.Client()
	return calc, cache
}

func TestGoogleCalculator_GetDistancesFromPointSendsRequiredHeadersAndParsesStream(t *testing.T) {
	var captured googleMatrixRequest
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "test-api-key" {
			t.Fatalf("X-Goog-Api-Key = %q", got)
		}
		if got := r.Header.Get("X-Goog-FieldMask"); got != googleRouteMatrixFieldMask {
			t.Fatalf("X-Goog-FieldMask = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":1200,"duration":"300s"}` + "\n"))
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":1,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":2400,"duration":"600.5s"}` + "\n"))
	})

	results, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{
		{Lat: 35.1, Lng: -79.1},
		{Lat: 35.2, Lng: -79.2},
	})
	if err != nil {
		t.Fatalf("GetDistancesFromPoint() error = %v", err)
	}
	if len(captured.Origins) != 1 || len(captured.Destinations) != 2 {
		t.Fatalf("origins/destinations = %d/%d, want 1/2", len(captured.Origins), len(captured.Destinations))
	}
	if captured.TravelMode != "DRIVE" {
		t.Fatalf("TravelMode = %q, want DRIVE", captured.TravelMode)
	}
	if captured.RoutingPreference != "TRAFFIC_UNAWARE" {
		t.Fatalf("RoutingPreference = %q, want TRAFFIC_UNAWARE", captured.RoutingPreference)
	}
	if results[0].DistanceMeters != 1200 || results[0].DurationSecs != 300 {
		t.Fatalf("result[0] = %+v, want 1200m/300s", results[0])
	}
	if results[1].DistanceMeters != 2400 || results[1].DurationSecs != 600.5 {
		t.Fatalf("result[1] = %+v, want 2400m/600.5s", results[1])
	}
}

func TestGoogleCalculator_BatchesDestinationsUnderElementLimit(t *testing.T) {
	requests := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var captured googleMatrixRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(captured.Origins)*len(captured.Destinations) > googleRouteMatrixMaxElements {
			t.Fatalf("request elements = %d, exceeds %d", len(captured.Origins)*len(captured.Destinations), googleRouteMatrixMaxElements)
		}
		for i := range captured.Destinations {
			_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":` + intToString(i) + `,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":100,"duration":"10s"}` + "\n"))
		}
	})

	destinations := make([]models.Coordinates, 700)
	for i := range destinations {
		destinations[i] = models.Coordinates{Lat: 36 + float64(i)*0.001, Lng: -79}
	}
	if _, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, destinations); err != nil {
		t.Fatalf("GetDistancesFromPoint() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestGoogleCalculator_GetDistanceMatrixBatchesOriginDestinationBlocks(t *testing.T) {
	requests := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		var captured googleMatrixRequest
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		elements := len(captured.Origins) * len(captured.Destinations)
		if elements > googleRouteMatrixMaxElements {
			t.Fatalf("request elements = %d, exceeds %d", elements, googleRouteMatrixMaxElements)
		}
		for originIndex := range captured.Origins {
			for destIndex := range captured.Destinations {
				_, _ = w.Write([]byte(`{"originIndex":` + strconv.Itoa(originIndex) + `,"destinationIndex":` + strconv.Itoa(destIndex) + `,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":100,"duration":"10s"}` + "\n"))
			}
		}
	})

	points := make([]models.Coordinates, 30)
	for i := range points {
		points[i] = models.Coordinates{Lat: 35 + float64(i)*0.001, Lng: -79}
	}
	if _, err := calc.GetDistanceMatrix(context.Background(), points); err != nil {
		t.Fatalf("GetDistanceMatrix() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 matrix block requests", requests)
	}
}

func TestGoogleCalculator_ReturnsElementFailure(t *testing.T) {
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{"code":5,"message":"route not found"},"condition":"ROUTE_NOT_FOUND"}` + "\n"))
	})

	_, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err == nil || !strings.Contains(err.Error(), "route not found") {
		t.Fatalf("error = %v, want route not found", err)
	}
}

func TestGoogleCalculator_RetriesRateLimitAfterProviderDelay(t *testing.T) {
	requests := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":1200,"duration":"300s"}` + "\n"))
	})

	started := time.Now()
	results, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err != nil {
		t.Fatalf("GetDistancesFromPoint() error = %v", err)
	}
	if len(results) != 1 || results[0].DistanceMeters != 1200 {
		t.Fatalf("results = %#v, want one 1200m result", results)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("elapsed = %s, want at least 1s Retry-After delay", elapsed)
	}
}

func TestGoogleCalculator_BoundsTransientRetries(t *testing.T) {
	requests := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := calc.GetDistancesFromPoint(ctx, models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err == nil {
		t.Fatal("GetDistancesFromPoint() error = nil, want transient provider error")
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestGoogleCalculator_RetriesNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":1200,"duration":"300s"}` + "\n"))
	}))
	t.Cleanup(server.Close)

	requests := 0
	transport := server.Client().Transport
	calc := NewGoogleCalculator(newGoogleTestCache(), func() (string, error) { return "test-api-key", nil }).(*googleCalculator)
	calc.endpoint = server.URL
	calc.httpClient = &http.Client{Transport: googleRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, errors.New("temporary network failure")
		}
		return transport.RoundTrip(request)
	})}

	results, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err != nil {
		t.Fatalf("GetDistancesFromPoint() error = %v", err)
	}
	if len(results) != 1 || requests != 2 {
		t.Fatalf("results/requests = %d/%d, want 1/2", len(results), requests)
	}
}

func TestGoogleCalculator_DoesNotRetryHTTP500AndPreservesPrivateBoundedMetadata(t *testing.T) {
	requests := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("provider-secret:" + strings.Repeat("x", 8192) + "uncaptured-tail"))
	})

	_, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err == nil {
		t.Fatal("GetDistancesFromPoint() error = nil, want permanent provider error")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if strings.Contains(err.Error(), "provider-secret") {
		t.Fatalf("public error contains upstream body: %v", err)
	}
	var httpErr *googleHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T, want retained *googleHTTPError metadata", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("StatusCode = %d, want 500", httpErr.StatusCode)
	}
	if httpErr.RetryAfter <= 0 || httpErr.RetryAfter > 2*time.Second {
		t.Fatalf("RetryAfter = %s, want parsed future HTTP-date", httpErr.RetryAfter)
	}
	if len(httpErr.Body) != providerErrorBodyLimit {
		t.Fatalf("body length = %d, want %d", len(httpErr.Body), providerErrorBodyLimit)
	}
	if strings.Contains(httpErr.Body, "uncaptured-tail") {
		t.Fatalf("body captured data beyond limit: %q", httpErr.Body)
	}
}

func TestGoogleCalculator_PrewarmContinuesAfterTransientBlockFailure(t *testing.T) {
	var mu sync.Mutex
	requestsByOrigin := make(map[string]int)
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		var request googleMatrixRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		origin := fmt.Sprintf("%.0f", request.Origins[0].Waypoint.Location.LatLng.Latitude)
		mu.Lock()
		requestsByOrigin[origin]++
		mu.Unlock()
		if origin == "35" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":1200,"duration":"300s"}` + "\n"))
	})

	err := calc.PrewarmPairs(context.Background(), []DistancePair{
		{Origin: models.Coordinates{Lat: 35, Lng: -79}, Destination: models.Coordinates{Lat: 35.1, Lng: -79}},
		{Origin: models.Coordinates{Lat: 36, Lng: -79}, Destination: models.Coordinates{Lat: 36.1, Lng: -79}},
	})
	if err == nil {
		t.Fatal("PrewarmPairs() error = nil, want retained transient provider error")
	}
	mu.Lock()
	defer mu.Unlock()
	if requestsByOrigin["35"] != 3 {
		t.Fatalf("failed-origin requests = %d, want 3", requestsByOrigin["35"])
	}
	if requestsByOrigin["36"] != 1 {
		t.Fatalf("remaining-origin requests = %d, want 1", requestsByOrigin["36"])
	}
}

func TestGoogleCalculator_MissingAPIKeyReturnsTypedError(t *testing.T) {
	calc := NewGoogleCalculator(newGoogleTestCache(), func() (string, error) {
		return "", nil
	})
	_, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestGoogleCalculator_MissingAPIKeyFailsBeforeUsingCache(t *testing.T) {
	cache := newGoogleTestCache()

	origin := models.Coordinates{Lat: 35, Lng: -79}
	dest := models.Coordinates{Lat: 36, Lng: -79}
	if err := cache.Set(context.Background(), &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 1000,
		DurationSecs:   300,
	}); err != nil {
		t.Fatalf("seed distance cache: %v", err)
	}

	calc := NewGoogleCalculator(cache, func() (string, error) {
		return "", nil
	})

	if _, err := calc.GetDistance(context.Background(), origin, dest); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("GetDistance() error = %v, want ErrProviderNotConfigured", err)
	}
	if _, err := calc.GetDistancesFromPoint(context.Background(), origin, []models.Coordinates{dest}); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("GetDistancesFromPoint() error = %v, want ErrProviderNotConfigured", err)
	}
	if _, err := calc.GetDistanceMatrix(context.Background(), []models.Coordinates{origin, dest}); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("GetDistanceMatrix() error = %v, want ErrProviderNotConfigured", err)
	}
}

func intToString(v int) string {
	return strconv.Itoa(v)
}
