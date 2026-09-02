package geocoding

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const validNominatimResult = `[{"lat":"35.9","lon":"-79.1","display_name":"Result","address":{"city":"Chapel Hill","state":"North Carolina","country_code":"us"}}]`

type nominatimRoundTripFunc func(*http.Request) (*http.Response, error)

func (f nominatimRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type nominatimReadErrorBody struct {
	data          []byte
	done          bool
	errorWithData bool
}

func (b *nominatimReadErrorBody) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		n := copy(p, b.data)
		if b.errorWithData {
			return n, io.ErrUnexpectedEOF
		}
		return n, nil
	}
	return 0, io.ErrUnexpectedEOF
}

func (b *nominatimReadErrorBody) Close() error {
	return nil
}

func newTestNominatimGeocoder(t *testing.T, handler http.HandlerFunc) *nominatimGeocoder {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rateLimiter := time.NewTicker(time.Millisecond)
	t.Cleanup(rateLimiter.Stop)
	return newNominatimGeocoder(server.URL, server.Client(), rateLimiter)
}

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
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	result, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
	if result != nil {
		t.Fatalf("GeocodeWithRetry() result = %#v, want nil", result)
	}
	if !errors.Is(err, ErrNoGeocodingResults) {
		t.Fatalf("GeocodeWithRetry() error = %v, want ErrNoGeocodingResults", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestNominatimGeocodeWithRetry_HonorsRetryAfter(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(validNominatimResult))
	})

	started := time.Now()
	result, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
	if err != nil {
		t.Fatalf("GeocodeWithRetry() error = %v", err)
	}
	if result == nil || result.Coords.Lat != 35.9 {
		t.Fatalf("GeocodeWithRetry() result = %#v, want one result", result)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("elapsed = %s, want at least 1s Retry-After delay", elapsed)
	}
}

func TestNominatimGeocodeWithRetry_BoundsPersistentServerErrors(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "failed", http.StatusInternalServerError)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := g.GeocodeWithRetry(ctx, "823 Redfield Dr", 4)
	if result != nil || err == nil {
		t.Fatalf("GeocodeWithRetry() = %#v, %v, want nil, error", result, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GeocodeWithRetry() exceeded the three-attempt provider limit: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
}

func TestNominatimGeocodeWithRetry_RetriesNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(validNominatimResult))
	}))
	t.Cleanup(server.Close)

	requests := 0
	transport := server.Client().Transport
	client := &http.Client{Transport: nominatimRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return nil, errors.New("temporary network failure")
		}
		return transport.RoundTrip(request)
	})}
	ticker := time.NewTicker(time.Millisecond)
	t.Cleanup(ticker.Stop)
	g := newNominatimGeocoder(server.URL, client, ticker)

	result, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
	if err != nil {
		t.Fatalf("GeocodeWithRetry() error = %v", err)
	}
	if result == nil || requests != 2 {
		t.Fatalf("result/requests = %#v/%d, want result/2", result, requests)
	}
}

func TestNominatim_RetriesResponseBodyNetworkError(t *testing.T) {
	operations := []struct {
		name string
		run  func(*nominatimGeocoder) error
	}{
		{
			name: "geocode",
			run: func(g *nominatimGeocoder) error {
				_, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
				return err
			},
		},
		{
			name: "search",
			run: func(g *nominatimGeocoder) error {
				_, err := g.Search(context.Background(), "Chapel Hill", 5)
				return err
			},
		},
	}
	failures := []struct {
		name          string
		body          string
		errorWithData bool
	}{
		{name: "after partial JSON", body: `[{"lat":"35`},
		{name: "with complete JSON", body: validNominatimResult, errorWithData: true},
	}

	for _, operation := range operations {
		for _, failure := range failures {
			t.Run(operation.name+" "+failure.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(validNominatimResult))
				}))
				t.Cleanup(server.Close)

				requests := 0
				transport := server.Client().Transport
				client := &http.Client{Transport: nominatimRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     make(http.Header),
							Body: &nominatimReadErrorBody{
								data:          []byte(failure.body),
								errorWithData: failure.errorWithData,
							},
							Request: request,
						}, nil
					}
					return transport.RoundTrip(request)
				})}
				ticker := time.NewTicker(time.Millisecond)
				t.Cleanup(ticker.Stop)
				g := newNominatimGeocoder(server.URL, client, ticker)

				if err := operation.run(g); err != nil {
					t.Fatalf("provider call error = %v", err)
				}
				if requests != 2 {
					t.Fatalf("requests = %d, want 2", requests)
				}
			})
		}
	}
}

func TestNominatimGeocodeWithRetry_DoesNotRetryPermanentHTTPError(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", time.Now().Add(2*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("provider-secret:" + strings.Repeat("x", 8192) + "uncaptured-tail"))
	})

	result, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
	if result != nil || err == nil {
		t.Fatalf("GeocodeWithRetry() = %#v, %v, want nil, error", result, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	var geocodingErr *ErrGeocodingFailed
	if !errors.As(err, &geocodingErr) {
		t.Fatalf("error = %T, want *ErrGeocodingFailed", err)
	}
	if geocodingErr.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, want 400", geocodingErr.HTTPStatus)
	}
	if geocodingErr.RetryAfter <= 0 || geocodingErr.RetryAfter > 2*time.Second {
		t.Fatalf("RetryAfter = %s, want parsed future HTTP-date", geocodingErr.RetryAfter)
	}
	if len(geocodingErr.ResponseBody) != providerBodyLimit {
		t.Fatalf("ResponseBody length = %d, want %d", len(geocodingErr.ResponseBody), providerBodyLimit)
	}
	if strings.Contains(geocodingErr.ResponseBody, "uncaptured-tail") {
		t.Fatalf("ResponseBody captured data beyond limit: %q", geocodingErr.ResponseBody)
	}
	if strings.Contains(err.Error(), "provider-secret") {
		t.Fatalf("public error contains upstream body: %v", err)
	}
}

func TestNominatimGeocodeWithRetry_SaturatesOversizedRetryAfter(t *testing.T) {
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(maxNominatimRetryAfter/time.Second)+1, 10))
		http.Error(w, "permanent", http.StatusBadRequest)
	})

	_, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3)
	var geocodingErr *ErrGeocodingFailed
	if !errors.As(err, &geocodingErr) {
		t.Fatalf("error = %T, want *ErrGeocodingFailed", err)
	}
	if geocodingErr.RetryAfter != maxNominatimRetryAfter {
		t.Fatalf("RetryAfter = %s, want saturation at %s", geocodingErr.RetryAfter, maxNominatimRetryAfter)
	}
}

func TestNominatimGeocodeWithRetry_DoesNotRetryMalformedResult(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`[{"lat":"not-a-number","lon":"-79.1"}]`))
	})

	if _, err := g.GeocodeWithRetry(context.Background(), "823 Redfield Dr", 3); err == nil {
		t.Fatal("GeocodeWithRetry() error = nil, want malformed-result error")
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestNominatimSearch_RetriesTransientFailure(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(validNominatimResult))
	})

	results, err := g.Search(context.Background(), "Chapel Hill", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 || results[0].Coords.Lat != 35.9 {
		t.Fatalf("Search() results = %#v, want one result", results)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestNominatimSearch_DoesNotRetryPermanentHTTPError(t *testing.T) {
	var requests atomic.Int32
	g := newTestNominatimGeocoder(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	})

	if _, err := g.Search(context.Background(), "Chapel Hill", 5); err == nil {
		t.Fatal("Search() error = nil, want permanent provider error")
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}
