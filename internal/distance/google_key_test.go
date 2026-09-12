package distance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"sync"
	"testing"
)

func TestGoogleMapsKeyDatabaseReplacementAndDeletion(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	first, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	}()
	second, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	var mu sync.Mutex
	var used []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		used = append(used, r.Header.Get("X-Goog-Api-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"originIndex":0,"destinationIndex":0,"distanceMeters":100,"duration":"60s","status":{"code":0},"condition":"ROUTE_EXISTS"}]`))
	}))
	defer provider.Close()
	calculator := NewGoogleCalculator(second.DistanceCache(), second.Settings().GoogleMapsKey).(*googleCalculator)
	calculator.endpoint = provider.URL
	calculator.httpClient = provider.Client()
	a, b := models.Coordinates{Lat: 38, Lng: -77}, models.Coordinates{Lat: 39, Lng: -77}
	t.Setenv("GOOGLE_MAPS_API_KEY", "ignored-env-secret")
	if _, err := calculator.GetDistance(t.Context(), a, b); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("missing key error=%v", err)
	}
	for _, key := range []string{"first-synthetic-secret", "replacement-synthetic-secret"} {
		if err := first.Settings().SetGoogleMapsKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		if _, err := calculator.fetchMatrixOnce(t.Context(), []models.Coordinates{a}, []models.Coordinates{b}); err != nil {
			t.Fatal(err)
		}
	}
	// Warm the actual cache, then delete. Missing credentials must fail before cached distances.
	if _, err := calculator.GetDistance(t.Context(), a, b); err != nil {
		t.Fatal(err)
	}
	if err := first.Settings().DeleteGoogleMapsKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := calculator.GetDistance(t.Context(), a, b); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("deleted key error=%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(used) != 3 || used[0] != "first-synthetic-secret" || used[1] != "replacement-synthetic-secret" || used[2] != "replacement-synthetic-secret" {
		t.Fatal("provider did not receive only current database credentials")
	}
}

func TestGoogleMapsKeyLookupUsesRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	calc := NewGoogleCalculator(newMockDistanceCache(), func(received context.Context) (string, error) {
		called = true
		if received != ctx {
			t.Error("credential lookup lost request context")
		}
		return "", received.Err()
	})
	_, err := calc.GetDistance(ctx, models.Coordinates{Lat: 1}, models.Coordinates{Lat: 2})
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("called=%t err=%v", called, err)
	}
}

func TestGoogleMapsKeyRedactedFromProviderErrors(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{"status", 200, `[{"originIndex":0,"destinationIndex":0,"status":{"code":7,"message":"invalid test-api-key"}}]`},
		{"duration", 200, `[{"originIndex":0,"destinationIndex":0,"duration":"test-api-key"}]`},
		{"http", 403, `invalid test-api-key`},
	} {
		t.Run(response.name, func(t *testing.T) {
			calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			})
			_, err := calc.fetchMatrixOnce(t.Context(), []models.Coordinates{{Lat: 1}}, []models.Coordinates{{Lat: 2}})
			if err == nil || strings.Contains(err.Error(), "test-api-key") {
				t.Fatalf("unsafe provider error=%v", err)
			}
			if e, ok := errors.AsType[*googleHTTPError](err); ok && strings.Contains(e.Body, "test-api-key") {
				t.Fatal("provider body retained key")
			}
		})
	}
}
