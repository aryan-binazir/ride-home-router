package geocoding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFormatAddressLabel(t *testing.T) {
	tests := []struct {
		name     string
		result   nominatimResponse
		expected string
	}{
		{
			name: "street address drops verbose neighborhood and county details",
			result: nominatimResponse{
				DisplayName: "120, South Peak Drive, Whispering Hills, Wildwood Springs, Carrboro, Orange County, North Carolina, 27510, United States",
				Address: nominatimAddress{
					HouseNumber: "120",
					Road:        "South Peak Drive",
					Suburb:      "Whispering Hills",
					City:        "Carrboro",
					County:      "Orange County",
					State:       "North Carolina",
					Postcode:    "27510",
					CountryCode: "us",
				},
			},
			expected: "120 South Peak Drive, Carrboro, NC 27510",
		},
		{
			name: "named places fall back to the place name when no street exists",
			result: nominatimResponse{
				DisplayName: "Raleigh-Durham International Airport, Morrisville, Wake County, North Carolina, 27560, United States",
				Name:        "Raleigh-Durham International Airport",
				Address: nominatimAddress{
					Amenity:     "Raleigh-Durham International Airport",
					City:        "Morrisville",
					State:       "North Carolina",
					Postcode:    "27560",
					CountryCode: "us",
				},
			},
			expected: "Raleigh-Durham International Airport, Morrisville, NC 27560",
		},
		{
			name: "fallback trims display name when structured fields are unavailable",
			result: nominatimResponse{
				DisplayName: "10 Downing Street, Westminster, London, Greater London, England, SW1A 2AA, United Kingdom",
			},
			expected: "10 Downing Street, Westminster, London, Greater London",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAddressLabel(tt.result); got != tt.expected {
				t.Fatalf("formatAddressLabel() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestNominatimGeocodeWithRetry_NoResultsReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(server.Close)

	rateLimiter := time.NewTicker(time.Millisecond)
	t.Cleanup(rateLimiter.Stop)
	g := newNominatimGeocoder(server.URL, server.Client(), rateLimiter)

	result, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 1)
	if result != nil {
		t.Fatalf("GeocodeWithRetry() result = %#v, want nil", result)
	}
	if !errors.Is(err, ErrNoGeocodingResults) {
		t.Fatalf("GeocodeWithRetry() error = %v, want ErrNoGeocodingResults", err)
	}
}
