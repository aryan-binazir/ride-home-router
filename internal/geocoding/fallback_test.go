package geocoding

import (
	"context"
	"errors"
	"testing"

	"ride-home-router/internal/models"
)

type stubGeocoder struct {
	geocodeFunc func(context.Context, string) (*GeocodingResult, error)
	searchFunc  func(context.Context, string, int) ([]GeocodingResult, error)
}

func (g stubGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	return g.geocodeFunc(ctx, address)
}

func (g stubGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	return g.geocodeFunc(ctx, address)
}

func (g stubGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	return g.searchFunc(ctx, query, limit)
}

func TestFallbackGeocoder_GeocodeFallsBackOnNoResults(t *testing.T) {
	expected := &GeocodingResult{
		Coords:           models.Coordinates{Lat: 35.2183, Lng: -77.6510},
		DisplayName:      "823 REDFIELD RD, KINSTON, NC, 28504",
		FormattedAddress: "823 Redfield Rd, Kinston, NC 28504",
	}

	g := &fallbackGeocoder{
		primary: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) {
				return nil, &ErrGeocodingFailed{Address: "823 Redfield Dr, Kinston, NC 28504", Reason: "no results found"}
			},
			searchFunc: func(context.Context, string, int) ([]GeocodingResult, error) { return nil, nil },
		},
		fallback: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) { return expected, nil },
			searchFunc:  func(context.Context, string, int) ([]GeocodingResult, error) { return nil, nil },
		},
	}

	result, err := g.Geocode(context.Background(), "823 Redfield Dr, Kinston, NC 28504")
	if err != nil {
		t.Fatalf("Geocode() err = %v, want nil", err)
	}
	if result != expected {
		t.Fatalf("Geocode() result = %#v, want %#v", result, expected)
	}
}

func TestFallbackGeocoder_GeocodeKeepsPrimaryErrorWhenFallbackFails(t *testing.T) {
	primaryErr := &ErrGeocodingFailed{Address: "823 Redfield Dr, Kinston, NC 28504", Reason: "no results found"}

	g := &fallbackGeocoder{
		primary: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) { return nil, primaryErr },
			searchFunc:  func(context.Context, string, int) ([]GeocodingResult, error) { return nil, nil },
		},
		fallback: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) {
				return nil, errors.New("fallback unavailable")
			},
			searchFunc: func(context.Context, string, int) ([]GeocodingResult, error) { return nil, nil },
		},
	}

	_, err := g.Geocode(context.Background(), "823 Redfield Dr, Kinston, NC 28504")
	if err != primaryErr {
		t.Fatalf("Geocode() err = %v, want primary err %v", err, primaryErr)
	}
}

func TestFallbackGeocoder_SearchFallsBackOnlyForSpecificUSAddress(t *testing.T) {
	fallbackCalled := false
	expected := []GeocodingResult{{FormattedAddress: "823 Redfield Rd, Kinston, NC 28504"}}

	g := &fallbackGeocoder{
		primary: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) { return nil, nil },
			searchFunc:  func(context.Context, string, int) ([]GeocodingResult, error) { return []GeocodingResult{}, nil },
		},
		fallback: stubGeocoder{
			geocodeFunc: func(context.Context, string) (*GeocodingResult, error) { return nil, nil },
			searchFunc: func(context.Context, string, int) ([]GeocodingResult, error) {
				fallbackCalled = true
				return expected, nil
			},
		},
	}

	results, err := g.Search(context.Background(), "823 Redfield Dr, Kinston, NC 28504", 5)
	if err != nil {
		t.Fatalf("Search() err = %v, want nil", err)
	}
	if !fallbackCalled {
		t.Fatal("Search() did not call fallback geocoder")
	}
	if len(results) != 1 || results[0].FormattedAddress != expected[0].FormattedAddress {
		t.Fatalf("Search() results = %#v, want %#v", results, expected)
	}

	fallbackCalled = false
	results, err = g.Search(context.Background(), "823 Redfield Dr", 5)
	if err != nil {
		t.Fatalf("Search() partial err = %v, want nil", err)
	}
	if fallbackCalled {
		t.Fatal("Search() called fallback for partial query")
	}
	if len(results) != 0 {
		t.Fatalf("Search() partial len = %d, want 0", len(results))
	}
}
