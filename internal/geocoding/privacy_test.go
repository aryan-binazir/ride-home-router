package geocoding

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeocoderLogsDoNotContainAddressOrResultNames(t *testing.T) {
	const address = "8123 Private Sentinel Ave, Boston, MA 02110"
	const displayName = "Secret Residence Display"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
	_, _ = geocodeWithRetry(context.Background(), address, 1, func(context.Context, string) (*GeocodingResult, error) {
		return nil, errors.New("retry failed for " + address)
	})

	output := logs.String()
	for _, private := range []string{address, displayName, "Private+Sentinel", "PRIVATE SENTINEL"} {
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
