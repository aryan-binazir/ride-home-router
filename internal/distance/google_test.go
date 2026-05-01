package distance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ride-home-router/internal/models"
	"ride-home-router/internal/sqlite"
)

func newTestGoogleCalculator(t *testing.T, handler http.HandlerFunc) (*googleCalculator, *sqlite.Store) {
	t.Helper()

	store, err := sqlite.New(filepath.Join(t.TempDir(), "google-distance-test.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.Close()
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	calc := NewGoogleCalculator(store.DistanceCache(), func() (string, error) {
		return "test-api-key", nil
	}).(*googleCalculator)
	calc.endpoint = server.URL
	return calc, store
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
		w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":1200,"duration":"300s"}` + "\n"))
		w.Write([]byte(`{"originIndex":0,"destinationIndex":1,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":2400,"duration":"600.5s"}` + "\n"))
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
			w.Write([]byte(`{"originIndex":0,"destinationIndex":` + intToString(i) + `,"status":{},"condition":"ROUTE_EXISTS","distanceMeters":100,"duration":"10s"}` + "\n"))
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

func TestGoogleCalculator_ReturnsElementFailure(t *testing.T) {
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"originIndex":0,"destinationIndex":0,"status":{"code":5,"message":"route not found"},"condition":"ROUTE_NOT_FOUND"}` + "\n"))
	})

	_, err := calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if err == nil || !strings.Contains(err.Error(), "route not found") {
		t.Fatalf("error = %v, want route not found", err)
	}
}

func TestGoogleCalculator_MissingAPIKeyReturnsTypedError(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "missing-key-test.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	calc := NewGoogleCalculator(store.DistanceCache(), func() (string, error) {
		return "", nil
	})
	_, err = calc.GetDistancesFromPoint(context.Background(), models.Coordinates{Lat: 35, Lng: -79}, []models.Coordinates{{Lat: 36, Lng: -79}})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestGoogleCalculator_MissingAPIKeyFailsBeforeUsingCache(t *testing.T) {
	store, err := sqlite.New(filepath.Join(t.TempDir(), "cached-missing-key-test.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	origin := models.Coordinates{Lat: 35, Lng: -79}
	dest := models.Coordinates{Lat: 36, Lng: -79}
	if err := store.DistanceCache().Set(context.Background(), &models.DistanceCacheEntry{
		Origin:         origin,
		Destination:    dest,
		DistanceMeters: 1000,
		DurationSecs:   300,
	}); err != nil {
		t.Fatalf("seed distance cache: %v", err)
	}

	calc := NewGoogleCalculator(store.DistanceCache(), func() (string, error) {
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
