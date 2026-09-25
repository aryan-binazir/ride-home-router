package importer

import (
	"context"
	"errors"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"strings"
	"sync"
	"testing"
)

func testGrid(t *testing.T, csv string) *Grid {
	t.Helper()
	grid, err := Parse(strings.NewReader(csv), FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return grid
}

func successfulTestGeocoder() *fakeGeocoder {
	return &fakeGeocoder{result: func(context.Context, string, int) (*geocoding.GeocodingResult, error) {
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40, Lng: -73}}, nil
	}}
}

type fakeGeocoder struct {
	geocoding.Geocoder
	mu     sync.Mutex
	calls  []string
	result func(context.Context, string, int) (*geocoding.GeocodingResult, error)
}

func (g *fakeGeocoder) GeocodeWithRetry(ctx context.Context, address string, retries int) (*geocoding.GeocodingResult, error) {
	g.mu.Lock()
	g.calls = append(g.calls, address)
	g.mu.Unlock()
	if g.result == nil {
		return nil, errors.New("unexpected geocode call")
	}
	return g.result(ctx, address, retries)
}

func (g *fakeGeocoder) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}
