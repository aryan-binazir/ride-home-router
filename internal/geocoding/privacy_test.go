package geocoding

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGeocoderLogsDoNotContainAddressOrResultNames(t *testing.T) {
	const address = "8123 Private Sentinel Ave, Boston, MA 02110"
	const displayName = "Secret Residence Display"
	const malformedAddress = "Malformed Coordinate Sentinel Rd, Boston, MA 02110"
	const malformedQuery = "Malformed Search Coordinate Sentinel"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("q")
		if query == malformedAddress || query == malformedQuery {
			//nolint:gosec // G705: the test server deliberately echoes the sentinel to prove production logs redact provider values.
			_, _ = w.Write([]byte(`[{"lat":` + strconv.Quote(query) + `,"lon":"-71","display_name":"Result","address":{}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"lat":"42","lon":"-71","display_name":"` + displayName + `","address":{}}]`))
	}))
	t.Cleanup(server.Close)

	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	ticker := time.NewTicker(time.Millisecond)
	t.Cleanup(ticker.Stop)
	nominatim := newNominatimGeocoder(server.URL, server.Client(), ticker)
	if _, err := nominatim.Geocode(context.Background(), address); err != nil {
		t.Fatalf("Nominatim Geocode() error = %v", err)
	}
	if _, err := nominatim.Geocode(context.Background(), malformedAddress); err == nil {
		t.Fatal("Nominatim Geocode() error = nil, want invalid coordinate error")
	}
	if results, err := nominatim.Search(context.Background(), malformedQuery, 5); err != nil || len(results) != 0 {
		t.Fatalf("Nominatim Search() = (%v, %v), want (empty, nil)", results, err)
	}
	_, _ = geocodeWithRetry(context.Background(), address, 1, func(context.Context, string) (*GeocodingResult, error) {
		return nil, errors.New("retry failed for " + address)
	})

	output := logs.String()
	for _, private := range []string{address, displayName, malformedAddress, malformedQuery, "Private+Sentinel", "PRIVATE SENTINEL"} {
		if strings.Contains(output, private) {
			t.Fatalf("logs contain private value %q: %s", private, output)
		}
	}
	for _, want := range []string{"outcome=success", "status=200", "retries=1", "duration="} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs missing %q: %s", want, output)
		}
	}
}

func TestGeocodingFailureErrorDoesNotContainAddress(t *testing.T) {
	const address = "8123 Private Error Sentinel Ave, Boston, MA 02110"
	err := &ErrGeocodingFailed{
		address: address,
		Reason:  "no results found",
		Cause:   ErrNoGeocodingResults,
	}

	if strings.Contains(err.Error(), address) || strings.Contains(err.Error(), "Private Error Sentinel") {
		t.Fatalf("public error contains private address: %v", err)
	}
	if !errors.Is(err, ErrNoGeocodingResults) {
		t.Fatalf("error no longer unwraps to ErrNoGeocodingResults: %v", err)
	}
}

func TestNominatimTransportFailureDoesNotContainAddress(t *testing.T) {
	const address = "8123 Private Transport Sentinel Ave, Boston, MA 02110"
	client := &http.Client{
		Transport: nominatimRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, &url.Error{
				Op:  http.MethodGet,
				URL: request.URL.String(),
				Err: errors.New("connection refused"),
			}
		}),
	}
	ticker := time.NewTicker(time.Millisecond)
	t.Cleanup(ticker.Stop)
	nominatim := newNominatimGeocoder("https://nominatim.example", client, ticker)
	_, err := nominatim.Geocode(context.Background(), address)
	if err == nil {
		t.Fatal("Nominatim Geocode() error = nil, want transport error")
	}

	for current := error(err); current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), address) || strings.Contains(current.Error(), "Private+Transport+Sentinel") {
			t.Fatalf("error chain contains private address: %v", current)
		}
	}
}
