package geocoding

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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
