package geocoding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func staticKey(key string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return key, nil }
}

func TestGoogleGeocodeReturnsCoordinatesAndLabelWithoutCountrySuffix(t *testing.T) {
	var gotQuery map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = map[string]string{
			"address": r.URL.Query().Get("address"),
			"key":     r.URL.Query().Get("key"),
			"region":  r.URL.Query().Get("region"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"OK","results":[{"formatted_address":"123 Main St, Boston, MA 02110, USA","geometry":{"location":{"lat":42.3601,"lng":-71.0589}}}]}`))
	}))
	t.Cleanup(server.Close)

	geocoder := newGoogleGeocoder(staticKey("test-key"), nil, server.Client(), server.URL, server.URL)
	result, err := geocoder.Geocode(context.Background(), "123 Main St, Boston")
	if err != nil {
		t.Fatalf("Geocode() error = %v", err)
	}
	if result.Coords.Lat != 42.3601 || result.Coords.Lng != -71.0589 {
		t.Fatalf("Geocode() coords = %+v, want 42.3601,-71.0589", result.Coords)
	}
	if result.Label() != "123 Main St, Boston, MA 02110" {
		t.Fatalf("Geocode() label = %q, want country suffix removed", result.Label())
	}
	if gotQuery["address"] != "123 Main St, Boston" || gotQuery["key"] != "test-key" || gotQuery["region"] != "us" {
		t.Fatalf("request query = %v, want address, key and region=us", gotQuery)
	}
}

type recordingGate struct {
	waits    int
	deferred []time.Duration
}

func (g *recordingGate) Wait(context.Context) error { g.waits++; return nil }
func (g *recordingGate) Defer(_ context.Context, d time.Duration) error {
	g.deferred = append(g.deferred, d)
	return nil
}

func googleStatusServer(t *testing.T, httpStatus int, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestGoogleGeocodeClassifiesProviderStatuses(t *testing.T) {
	cases := []struct {
		name          string
		httpStatus    int
		body          string
		wantSentinel  error
		wantRetryable bool
		wantDeferred  bool
	}{
		{"zero results are a bad address", http.StatusOK, `{"status":"ZERO_RESULTS","results":[]}`, ErrNoGeocodingResults, false, false},
		{"request denied means the key is not usable", http.StatusOK, `{"status":"REQUEST_DENIED","error_message":"The provided API key is invalid."}`, ErrNotConfigured, true, false},
		{"daily limit means billing or key problems", http.StatusOK, `{"status":"OVER_DAILY_LIMIT"}`, ErrNotConfigured, true, false},
		{"query limit is transient and pauses the shared gate", http.StatusOK, `{"status":"OVER_QUERY_LIMIT"}`, nil, true, true},
		{"server error is transient", http.StatusInternalServerError, `{"status":"UNKNOWN_ERROR"}`, nil, true, false},
		{"http 429 pauses the shared gate", http.StatusTooManyRequests, `{}`, nil, true, true},
		{"invalid request is permanent", http.StatusOK, `{"status":"INVALID_REQUEST"}`, nil, false, false},
		{"denial delivered with HTTP 403 still means not configured", http.StatusForbidden, `{"status":"REQUEST_DENIED","error_message":"denied"}`, ErrNotConfigured, true, false},
		{"quota delivered with HTTP 429 is transient and pauses the gate", http.StatusTooManyRequests, `{"status":"OVER_QUERY_LIMIT"}`, nil, true, true},
		{"zero results delivered with HTTP 404 is still a bad address", http.StatusNotFound, `{"status":"ZERO_RESULTS","results":[]}`, ErrNoGeocodingResults, false, false},
		{"unreadable body falls back to the HTTP status", http.StatusBadGateway, `<html>bad gateway</html>`, nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := googleStatusServer(t, tc.httpStatus, tc.body)
			gate := &recordingGate{}
			geocoder := newGoogleGeocoder(staticKey("k"), gate, server.Client(), server.URL, server.URL)
			_, err := geocoder.Geocode(context.Background(), "1 Test Way")
			if err == nil {
				t.Fatal("Geocode() error = nil, want failure")
			}
			if tc.wantSentinel != nil && !errors.Is(err, tc.wantSentinel) {
				t.Fatalf("Geocode() error = %v, want %v in chain", err, tc.wantSentinel)
			}
			failure, ok := errors.AsType[*ErrGeocodingFailed](err)
			if !ok {
				t.Fatalf("Geocode() error type = %T, want *ErrGeocodingFailed", err)
			}
			if failure.Retryable() != tc.wantRetryable {
				t.Fatalf("Retryable() = %v, want %v", failure.Retryable(), tc.wantRetryable)
			}
			if (len(gate.deferred) > 0) != tc.wantDeferred {
				t.Fatalf("gate deferred = %v, want deferred=%v", gate.deferred, tc.wantDeferred)
			}
			if gate.waits != 1 {
				t.Fatalf("gate waits = %d, want 1", gate.waits)
			}
		})
	}
}

func TestGoogleGeocodeWithoutKeyIsNotConfiguredAndMakesNoRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++ }))
	t.Cleanup(server.Close)
	geocoder := newGoogleGeocoder(staticKey("  "), &recordingGate{}, server.Client(), server.URL, server.URL)
	_, err := geocoder.Geocode(context.Background(), "1 Test Way")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Geocode() error = %v, want ErrNotConfigured", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}
	storeErr := errors.New("database down")
	geocoder = newGoogleGeocoder(func(context.Context) (string, error) { return "", storeErr }, &recordingGate{}, server.Client(), server.URL, server.URL)
	_, err = geocoder.Geocode(context.Background(), "1 Test Way")
	failure, ok := errors.AsType[*ErrGeocodingFailed](err)
	if !ok || !failure.Retryable() || !errors.Is(err, storeErr) {
		t.Fatalf("key store failure = %v, want retryable ErrGeocodingFailed wrapping the cause", err)
	}
}

func TestGoogleSearchSuggestsAddressesFromPlacesAutocomplete(t *testing.T) {
	var gotHeader, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Goog-Api-Key")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		if r.Method != http.MethodPost || r.URL.Query().Get("key") != "" {
			t.Errorf("request = %s %s, want POST without key in URL", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"suggestions":[
			{"placePrediction":{"placeId":"a","text":{"text":"123 Main St, Boston, MA 02110, USA"}}},
			{"queryPrediction":{"text":{"text":"main street pizza"}}},
			{"placePrediction":{"placeId":"b","text":{"text":"   "}}},
			{"placePrediction":{"placeId":"c","text":{"text":"123 Main St, Cambridge, MA 02139, USA"}}},
			{"placePrediction":{"placeId":"d","text":{"text":"123 Main Ave, Somerville, MA, USA"}}}]}`))
	}))
	t.Cleanup(server.Close)

	geocoder := newGoogleGeocoder(staticKey("places-key"), &recordingGate{}, server.Client(), server.URL, server.URL)
	results, err := geocoder.Search(context.Background(), "123 Main", 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	labels := make([]string, 0, len(results))
	for _, result := range results {
		labels = append(labels, result.Label())
	}
	want := []string{"123 Main St, Boston, MA 02110", "123 Main St, Cambridge, MA 02139"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Fatalf("Search() labels = %v, want %v", labels, want)
	}
	if gotHeader != "places-key" {
		t.Fatalf("X-Goog-Api-Key = %q, want the key in the header", gotHeader)
	}
	if !strings.Contains(gotBody, `"input":"123 Main"`) || !strings.Contains(gotBody, `"regionCode":"us"`) {
		t.Fatalf("request body = %s, want input and regionCode", gotBody)
	}
}

func TestGoogleSearchRetriesTransientFailureThenReportsPermanentDenial(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE","message":"try later"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"suggestions":[{"placePrediction":{"placeId":"a","text":{"text":"1 Test Way, Boston, MA, USA"}}}]}`))
	}))
	t.Cleanup(server.Close)
	gate := &recordingGate{}
	geocoder := newGoogleGeocoder(staticKey("k"), gate, server.Client(), server.URL, server.URL)
	results, err := geocoder.Search(context.Background(), "1 Test", 5)
	if err != nil || len(results) != 1 || calls != 2 {
		t.Fatalf("Search() = (%v, %v) after %d calls, want one result after a retry", results, err, calls)
	}
	if len(gate.deferred) != 1 {
		t.Fatalf("gate deferred = %v, want one cooldown from the 503", gate.deferred)
	}

	denied := googleStatusServer(t, http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"Places API (New) has not been used in project 12345"}}`)
	geocoder = newGoogleGeocoder(staticKey("k"), &recordingGate{}, denied.Client(), denied.URL, denied.URL)
	_, err = geocoder.Search(context.Background(), "1 Test", 5)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Search() error = %v, want ErrNotConfigured", err)
	}
	if strings.Contains(err.Error(), "12345") || strings.Contains(err.Error(), "project") {
		t.Fatalf("error exposes provider message: %v", err)
	}
}

func TestGoogleGeocoderNeverExposesAddressKeyOrURL(t *testing.T) {
	const address = "8123 Private Sentinel Ave, Boston, MA 02110"
	const key = "AIzaSecretKeySentinel"
	var logs bytes.Buffer
	previousOutput, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(previousOutput); log.SetFlags(previousFlags) })

	refusing := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: errors.New("connection refused")}
	})}
	denied := googleStatusServer(t, http.StatusOK, `{"status":"REQUEST_DENIED","error_message":"key `+key+` is invalid for `+address+`"}`)
	placesDenied := googleStatusServer(t, http.StatusForbidden, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"key `+key+` denied for `+address+`"}}`)
	ok := googleStatusServer(t, http.StatusOK, `{"status":"OK","results":[{"formatted_address":"`+address+`, USA","geometry":{"location":{"lat":42.1,"lng":-71.1}}}]}`)
	placesOK := googleStatusServer(t, http.StatusOK, `{"suggestions":[{"placePrediction":{"placeId":"a","text":{"text":"`+address+`, USA"}}}]}`)

	var errs []error
	for _, geocoder := range []*googleGeocoder{
		newGoogleGeocoder(staticKey(key), &recordingGate{}, refusing, "https://maps.example/geocode", "https://places.example/autocomplete"),
		newGoogleGeocoder(staticKey(key), &recordingGate{}, denied.Client(), denied.URL, placesDenied.URL),
		newGoogleGeocoder(staticKey(key), &recordingGate{}, ok.Client(), ok.URL, placesOK.URL),
	} {
		_, err := geocoder.Geocode(context.Background(), address)
		errs = append(errs, err)
		_, err = geocoder.Search(context.Background(), address, 5)
		errs = append(errs, err)
	}

	private := []string{address, key, url.QueryEscape(address), url.QueryEscape(key), "Private+Sentinel", "maps.example", "places.example", "error_message", "is invalid"}
	for _, err := range errs {
		for current := err; current != nil; current = errors.Unwrap(current) {
			for _, value := range private {
				if strings.Contains(current.Error(), value) {
					t.Fatalf("error chain contains %q: %v", value, current)
				}
			}
		}
	}
	for _, value := range private {
		if strings.Contains(logs.String(), value) {
			t.Fatalf("logs contain %q:\n%s", value, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "outcome=success") || !strings.Contains(logs.String(), "outcome=request_failed") {
		t.Fatalf("logs missing outcome lines:\n%s", logs.String())
	}
}

func TestGoogleGeocodeWithRetryHonorsRetryAfterAndBoundsAttempts(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	gate := &recordingGate{}
	geocoder := newGoogleGeocoder(staticKey("k"), gate, server.Client(), server.URL, server.URL)
	started := time.Now()
	_, err := geocoder.GeocodeWithRetry(context.Background(), "1 Test Way", 5)
	if err == nil {
		t.Fatal("GeocodeWithRetry() error = nil, want persistent failure")
	}
	if calls != maxAttempts {
		t.Fatalf("calls = %d, want attempts bounded to %d", calls, maxAttempts)
	}
	if elapsed := time.Since(started); elapsed < 2*time.Second {
		t.Fatalf("elapsed = %s, want Retry-After honored between attempts", elapsed)
	}
	if len(gate.deferred) != maxAttempts || gate.deferred[0] < googleQuotaCooldown {
		t.Fatalf("gate deferred = %v, want a cooldown per 503", gate.deferred)
	}
}

func TestGoogleGeocodeWithRetryRetriesNetworkErrorsButNotBadAddresses(t *testing.T) {
	attempts := 0
	flaky := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: errors.New("connection reset")}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"OK","results":[{"formatted_address":"1 Test Way, Boston, MA, USA","geometry":{"location":{"lat":42,"lng":-71}}}]}`)), Request: request}, nil
	})}
	geocoder := newGoogleGeocoder(staticKey("k"), nil, flaky, "https://maps.example/geocode", "https://places.example/autocomplete")
	result, err := geocoder.GeocodeWithRetry(context.Background(), "1 Test Way", 3)
	if err != nil || attempts != 2 || result.Coords.Lat != 42 {
		t.Fatalf("GeocodeWithRetry() = (%+v, %v) after %d attempts, want success on the second", result, err, attempts)
	}

	zero := googleStatusServer(t, http.StatusOK, `{"status":"ZERO_RESULTS","results":[]}`)
	calls := 0
	counting := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return zero.Client().Transport.RoundTrip(request)
	})}
	geocoder = newGoogleGeocoder(staticKey("k"), nil, counting, zero.URL, zero.URL)
	if _, err := geocoder.GeocodeWithRetry(context.Background(), "Nowhere", 3); !errors.Is(err, ErrNoGeocodingResults) || calls != 1 {
		t.Fatalf("bad address: err=%v calls=%d, want ErrNoGeocodingResults after one call", err, calls)
	}

	malformed := googleStatusServer(t, http.StatusOK, `{"status":"OK","results":[{"formatted_address":"x","geometry":{"location":{"lat":123.4,"lng":-71}}}]}`)
	calls = 0
	counting = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return malformed.Client().Transport.RoundTrip(request)
	})}
	geocoder = newGoogleGeocoder(staticKey("k"), nil, counting, malformed.URL, malformed.URL)
	if _, err := geocoder.GeocodeWithRetry(context.Background(), "1 Test Way", 3); err == nil || calls != 1 {
		t.Fatalf("out-of-range coordinates: err=%v calls=%d, want a single permanent failure", err, calls)
	}
}

func TestRetryAfterClampsToFifteenMinutes(t *testing.T) {
	if got := parseRetryAfter("999999999"); got != maxRetryAfter {
		t.Fatalf("parseRetryAfter(huge) = %s, want %s", got, maxRetryAfter)
	}
	if got := parseRetryAfter("7"); got != 7*time.Second {
		t.Fatalf("parseRetryAfter(7) = %s", got)
	}
	if got := parseRetryAfter("garbage"); got != 0 {
		t.Fatalf("parseRetryAfter(garbage) = %s, want 0", got)
	}
}

func TestMain(m *testing.M) {
	retryBaseDelay = 5 * time.Millisecond
	os.Exit(m.Run())
}

func TestGoogleKeyProblemsFailFastWithoutRequestRetries(t *testing.T) {
	calls := 0
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"API key not valid","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`))
	}))
	t.Cleanup(denied.Close)
	geocoder := newGoogleGeocoder(staticKey("typo"), &recordingGate{}, denied.Client(), denied.URL, denied.URL)
	_, err := geocoder.Search(context.Background(), "1 Test", 5)
	if !errors.Is(err, ErrNotConfigured) || calls != 1 {
		t.Fatalf("Places 400 API_KEY_INVALID: err=%v calls=%d, want ErrNotConfigured after one call", err, calls)
	}
	if strings.Contains(err.Error(), "API key not valid") {
		t.Fatalf("error exposes provider message: %v", err)
	}

	calls = 0
	geocodeDenied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"status":"REQUEST_DENIED"}`))
	}))
	t.Cleanup(geocodeDenied.Close)
	geocoder = newGoogleGeocoder(staticKey("typo"), &recordingGate{}, geocodeDenied.Client(), geocodeDenied.URL, geocodeDenied.URL)
	_, err = geocoder.GeocodeWithRetry(context.Background(), "1 Test Way", 3)
	failure, ok := errors.AsType[*ErrGeocodingFailed](err)
	if !ok || !errors.Is(err, ErrNotConfigured) || calls != 1 || !failure.Retryable() {
		t.Fatalf("denied key: err=%v calls=%d, want one call, ErrNotConfigured, still Retryable for imports", err, calls)
	}

	calls = 0
	geocoder = newGoogleGeocoder(staticKey(""), &recordingGate{}, denied.Client(), denied.URL, denied.URL)
	if _, err := geocoder.Search(context.Background(), "1 Test", 5); !errors.Is(err, ErrNotConfigured) || calls != 0 {
		t.Fatalf("missing key search: err=%v calls=%d, want ErrNotConfigured and no request", err, calls)
	}
}

func TestGoogleGeocodeRejectsIncompleteResponses(t *testing.T) {
	for name, body := range map[string]string{
		"missing coordinates":             `{"status":"OK","results":[{"formatted_address":"x","geometry":{"location":{}}}]}`,
		"ok without results":              `{"status":"OK","results":[]}`,
		"ok with non-numeric coordinates": `{"status":"OK","results":[{"formatted_address":"x","geometry":{"location":{"lat":"invalid","lng":-71}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := googleStatusServer(t, http.StatusOK, body)
			geocoder := newGoogleGeocoder(staticKey("k"), nil, server.Client(), server.URL, server.URL)
			result, err := geocoder.Geocode(context.Background(), "1 Test Way")
			if err == nil || result != nil {
				t.Fatalf("Geocode() = (%+v, %v), want failure", result, err)
			}
		})
	}
	empty := googleStatusServer(t, http.StatusOK, `{"suggestions":[]}`)
	geocoder := newGoogleGeocoder(staticKey("k"), nil, empty.Client(), empty.URL, empty.URL)
	if results, err := geocoder.Search(context.Background(), "1 Test", 5); err != nil || len(results) != 0 {
		t.Fatalf("Search() with no suggestions = (%v, %v), want (empty, nil)", results, err)
	}
}

func TestRetryAfterAcceptsHTTPDatesAndIgnoresNegatives(t *testing.T) {
	if got := parseRetryAfter("-5"); got != 0 {
		t.Fatalf("parseRetryAfter(-5) = %s, want 0", got)
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 80*time.Second || got > 90*time.Second {
		t.Fatalf("parseRetryAfter(http date) = %s, want about 90s", got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Fatalf("parseRetryAfter(past date) = %s, want 0", got)
	}
}

func TestGoogleGeocodeWithRetryStopsWhenContextCancelsDuringBackoff(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	geocoder := newGoogleGeocoder(staticKey("k"), nil, server.Client(), server.URL, server.URL)
	_, err := geocoder.GeocodeWithRetry(ctx, "1 Test Way", 3)
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("cancelled backoff: err=%v calls=%d, want DeadlineExceeded after one call", err, calls)
	}
}
