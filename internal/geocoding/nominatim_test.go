package geocoding

import (
	"context"
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

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expectOK  bool
		expectLat float64
		expectLng float64
	}{
		{
			name:      "plain lat lng pair",
			input:     "35.7796,-78.6382",
			expectOK:  true,
			expectLat: 35.7796,
			expectLng: -78.6382,
		},
		{
			name:      "google maps at segment",
			input:     "https://www.google.com/maps/place/X/@35.7796,-78.6382,14z",
			expectOK:  true,
			expectLat: 35.7796,
			expectLng: -78.6382,
		},
		{
			name:      "google maps q parameter",
			input:     "https://maps.google.com/?q=35.7796,-78.6382",
			expectOK:  true,
			expectLat: 35.7796,
			expectLng: -78.6382,
		},
		{
			name:     "non coordinate input",
			input:    "120 South Peak Drive, Carrboro, NC 27510",
			expectOK: false,
		},
		{
			name:     "out of range coordinate input",
			input:    "135.7796,-78.6382",
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coords, ok := parseCoordinates(tt.input)
			if ok != tt.expectOK {
				t.Fatalf("parseCoordinates() ok = %v, want %v", ok, tt.expectOK)
			}
			if !ok {
				return
			}
			if coords.Lat != tt.expectLat || coords.Lng != tt.expectLng {
				t.Fatalf("parseCoordinates() = (%f,%f), want (%f,%f)", coords.Lat, coords.Lng, tt.expectLat, tt.expectLng)
			}
		})
	}
}

func TestGeocode_ParsedCoordinatesBypassAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	g := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}
	defer g.rateLimiter.Stop()

	result, err := g.Geocode(context.Background(), "https://maps.google.com/?q=35.7796,-78.6382")
	if err != nil {
		t.Fatalf("Geocode() err = %v, want nil", err)
	}
	if called {
		t.Fatal("Geocode() called external API for parsed coordinates, expected bypass")
	}
	if result.Coords.Lat != 35.7796 || result.Coords.Lng != -78.6382 {
		t.Fatalf("Geocode() coords = (%f,%f), want (35.7796,-78.6382)", result.Coords.Lat, result.Coords.Lng)
	}
}

func TestGeocode_AddressStillCallsAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"lat":"35.7796",
				"lon":"-78.6382",
				"display_name":"120 South Peak Drive, Carrboro, NC 27510, USA",
				"address":{
					"house_number":"120",
					"road":"South Peak Drive",
					"city":"Carrboro",
					"state":"North Carolina",
					"postcode":"27510",
					"country_code":"us"
				}
			}
		]`))
	}))
	defer server.Close()

	g := &nominatimGeocoder{
		baseURL: server.URL,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Millisecond),
	}
	defer g.rateLimiter.Stop()

	result, err := g.Geocode(context.Background(), "120 South Peak Drive, Carrboro, NC 27510")
	if err != nil {
		t.Fatalf("Geocode() err = %v, want nil", err)
	}
	if result.Coords.Lat != 35.7796 || result.Coords.Lng != -78.6382 {
		t.Fatalf("Geocode() coords = (%f,%f), want (35.7796,-78.6382)", result.Coords.Lat, result.Coords.Lng)
	}
}
