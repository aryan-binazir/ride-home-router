package geocoding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCensusSearch_ParsesResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"result": {
				"addressMatches": [{
					"coordinates": {"x": -77.651060566781, "y": 35.218356070087},
					"matchedAddress": "823 REDFIELD RD, KINSTON, NC, 28504",
					"addressComponents": {
						"fromAddress": "823",
						"streetName": "REDFIELD",
						"suffixType": "RD",
						"city": "KINSTON",
						"state": "NC",
						"zip": "28504"
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	g := newCensusGeocoder(server.URL, &http.Client{Timeout: 2 * time.Second})

	results, err := g.Search(context.Background(), "823 Redfield Dr, Kinston, NC 28504", 5)
	if err != nil {
		t.Fatalf("Search() err = %v, want nil", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search() len = %d, want 1", len(results))
	}
	if results[0].Coords.Lat != 35.218356070087 || results[0].Coords.Lng != -77.651060566781 {
		t.Fatalf("Search() coords = (%f,%f), want (35.218356070087,-77.651060566781)", results[0].Coords.Lat, results[0].Coords.Lng)
	}
	if results[0].FormattedAddress != "823 Redfield Rd, Kinston, NC 28504" {
		t.Fatalf("Search() formatted = %q, want %q", results[0].FormattedAddress, "823 Redfield Rd, Kinston, NC 28504")
	}
}

func TestCensusSearch_NoMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"addressMatches":[]}}`))
	}))
	defer server.Close()

	g := newCensusGeocoder(server.URL, &http.Client{Timeout: 2 * time.Second})

	results, err := g.Search(context.Background(), "missing", 5)
	if err != nil {
		t.Fatalf("Search() err = %v, want nil", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search() len = %d, want 0", len(results))
	}
}

func TestCensusGeocode_NoMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"addressMatches":[]}}`))
	}))
	defer server.Close()

	g := newCensusGeocoder(server.URL, &http.Client{Timeout: 2 * time.Second})

	_, err := g.Geocode(context.Background(), "missing")
	if !isNoResultsError(err) {
		t.Fatalf("Geocode() err = %v, want no results error", err)
	}
}
