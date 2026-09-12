package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"strings"
	"time"
)

const (
	googleGeocodeURL      = "https://maps.googleapis.com/maps/api/geocode/json"
	googleAutocompleteURL = "https://places.googleapis.com/v1/places:autocomplete"
	googleRegion          = "us"
	googleBodyLimit       = 256 << 10
)

// KeyFunc returns the current Google Maps API key; an empty key means unconfigured.
type KeyFunc func(context.Context) (string, error)

type googleGeocoder struct {
	apiKey          KeyFunc
	gate            RateGate
	httpClient      *http.Client
	geocodeURL      string
	autocompleteURL string
}

// NewGoogleGeocoder geocodes with the Google Geocoding API and suggests
// addresses with Places Autocomplete, sharing one cross-instance cooldown gate.
func NewGoogleGeocoder(apiKey KeyFunc, gate RateGate) Geocoder {
	return newGoogleGeocoder(apiKey, gate, &http.Client{Timeout: geocoderClientTimeout}, googleGeocodeURL, googleAutocompleteURL)
}

func newGoogleGeocoder(apiKey KeyFunc, gate RateGate, client *http.Client, geocodeURL, autocompleteURL string) *googleGeocoder {
	return &googleGeocoder{apiKey: apiKey, gate: gate, httpClient: client, geocodeURL: geocodeURL, autocompleteURL: autocompleteURL}
}

type googleGeocodeResponse struct {
	Status  string `json:"status"`
	Results []struct {
		FormattedAddress string `json:"formatted_address"`
		Geometry         struct {
			Location struct {
				Lat *float64 `json:"lat"`
				Lng *float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
	} `json:"results"`
}

// ErrNotConfigured means the Google Maps key is missing, rejected, or its
// project cannot bill; administrators fix it in Settings or the Cloud console.
var ErrNotConfigured = errors.New("geocoding: Google Maps API key is not configured")

func (g *googleGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	started := time.Now()
	key, err := g.currentKey(ctx)
	if err != nil {
		return nil, err
	}
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	query := url.Values{"address": {address}, "region": {googleRegion}, "key": {key}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.geocodeURL+"?"+query.Encode(), nil)
	if err != nil {
		return nil, &ErrGeocodingFailed{Reason: "request creation failed", Cause: err}
	}
	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Google geocode outcome=request_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider request failed", Cause: newProviderTransportError(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded googleGeocodeResponse
	decodeErr := decodeGoogle(resp.Body, &decoded)
	if decoded.Status == "" {
		// No recognisable Google status: fall back to the HTTP status.
		if resp.StatusCode != http.StatusOK {
			if err := g.deferProvider(ctx, resp.StatusCode, resp.Header.Get("Retry-After")); err != nil {
				return nil, err
			}
			log.Printf("[ERROR] Google geocode outcome=http_error status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
			return nil, &ErrGeocodingFailed{Reason: "provider returned an error", HTTPStatus: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
		}
		log.Printf("[ERROR] Google geocode outcome=decode_failed status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		if decodeErr == nil {
			decodeErr = errors.New("missing status")
		}
		return nil, &ErrGeocodingFailed{Reason: "malformed provider response", Cause: decodeErr}
	}
	switch decoded.Status {
	case "OK":
	case "ZERO_RESULTS":
		log.Printf("[GEOCODING] Google geocode outcome=zero_results duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "no results found", Cause: ErrNoGeocodingResults}
	case "REQUEST_DENIED", "OVER_DAILY_LIMIT":
		log.Printf("[ERROR] Google geocode outcome=denied duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider not configured", Cause: ErrNotConfigured, Configuration: true}
	case "OVER_QUERY_LIMIT":
		log.Printf("[ERROR] Google geocode outcome=quota duration=%s", time.Since(started).Round(time.Millisecond))
		if err := g.deferProvider(ctx, http.StatusTooManyRequests, resp.Header.Get("Retry-After")); err != nil {
			return nil, err
		}
		return nil, &ErrGeocodingFailed{Reason: "provider quota exceeded", HTTPStatus: http.StatusTooManyRequests, RetryAfter: googleQuotaCooldown, Temporary: true}
	case "UNKNOWN_ERROR":
		log.Printf("[ERROR] Google geocode outcome=unknown_error duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider temporarily unavailable", Temporary: true}
	default:
		log.Printf("[ERROR] Google geocode outcome=unexpected_status duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider rejected the request"}
	}
	if len(decoded.Results) == 0 {
		return nil, &ErrGeocodingFailed{Reason: "no results found", Cause: ErrNoGeocodingResults}
	}
	result := decoded.Results[0]
	loc := result.Geometry.Location
	if loc.Lat == nil || loc.Lng == nil || *loc.Lat < -90 || *loc.Lat > 90 || *loc.Lng < -180 || *loc.Lng > 180 {
		log.Printf("[ERROR] Google geocode outcome=invalid_coordinates duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "invalid coordinates"}
	}
	log.Printf("[GEOCODING] Google geocode outcome=success status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
	return &GeocodingResult{
		Coords:           models.Coordinates{Lat: *loc.Lat, Lng: *loc.Lng},
		DisplayName:      result.FormattedAddress,
		FormattedAddress: googleAddressLabel(result.FormattedAddress),
	}, nil
}

const googleQuotaCooldown = 5 * time.Second

func (g *googleGeocoder) currentKey(ctx context.Context) (string, error) {
	key, err := g.apiKey(ctx)
	if err != nil {
		return "", &ErrGeocodingFailed{Reason: "provider credentials unavailable", Cause: err, Configuration: true}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", &ErrGeocodingFailed{Reason: "provider not configured", Cause: ErrNotConfigured, Configuration: true}
	}
	return key, nil
}

func (g *googleGeocoder) wait(ctx context.Context) error {
	if g.gate == nil {
		return nil
	}
	if err := g.gate.Wait(ctx); err != nil {
		return &ErrGeocodingFailed{Reason: "provider pacing unavailable", Cause: err, HTTPStatus: http.StatusServiceUnavailable}
	}
	return nil
}

// deferProvider records a shared cooldown when Google asks every instance to back off.
func (g *googleGeocoder) deferProvider(ctx context.Context, status int, retryAfter string) error {
	if g.gate == nil || (status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable) {
		return nil
	}
	if err := g.gate.Defer(ctx, max(googleQuotaCooldown, parseRetryAfter(retryAfter))); err != nil {
		return &ErrGeocodingFailed{Reason: "provider pacing unavailable", Cause: err, HTTPStatus: http.StatusServiceUnavailable}
	}
	return nil
}

// googleAddressLabel drops the country suffix Google appends to US addresses.
func googleAddressLabel(formatted string) string {
	label := strings.TrimSpace(formatted)
	for _, suffix := range []string{", USA", ", United States"} {
		label = strings.TrimSuffix(label, suffix)
	}
	return label
}

func (g *googleGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	return geocodeWithRetry(ctx, address, maxRetries, g.Geocode)
}

type googleAutocompleteResponse struct {
	Suggestions []struct {
		PlacePrediction *struct {
			PlaceID string `json:"placeId"`
			Text    struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"placePrediction"`
	} `json:"suggestions"`
}

const googleMaxSuggestions = 10

// Search suggests addresses with Places Autocomplete; suggestions carry labels
// only, and coordinates come from Geocode when the address is saved.
func (g *googleGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	var results []GeocodingResult
	err := withRetry(ctx, maxAttempts, func(ctx context.Context) error {
		var err error
		results, err = g.searchOnce(ctx, query, limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (g *googleGeocoder) searchOnce(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	started := time.Now()
	key, err := g.currentKey(ctx)
	if err != nil {
		return nil, err
	}
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"input": query, "regionCode": googleRegion, "languageCode": "en"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.autocompleteURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, &ErrGeocodingFailed{Reason: "request creation failed", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", key)
	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Google search outcome=request_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider request failed", Cause: newProviderTransportError(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden, googleKeyRejected(resp):
		log.Printf("[ERROR] Google search outcome=denied status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider not configured", Cause: ErrNotConfigured, Configuration: true}
	default:
		if err := g.deferProvider(ctx, resp.StatusCode, resp.Header.Get("Retry-After")); err != nil {
			return nil, err
		}
		log.Printf("[ERROR] Google search outcome=http_error status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "provider returned an error", HTTPStatus: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}

	var decoded googleAutocompleteResponse
	if err := decodeGoogle(resp.Body, &decoded); err != nil {
		log.Printf("[ERROR] Google search outcome=decode_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Reason: "malformed provider response", Cause: err}
	}
	limit = max(1, min(limit, googleMaxSuggestions))
	results := make([]GeocodingResult, 0, limit)
	for _, suggestion := range decoded.Suggestions {
		if suggestion.PlacePrediction == nil {
			continue
		}
		label := googleAddressLabel(suggestion.PlacePrediction.Text.Text)
		if label == "" {
			continue
		}
		results = append(results, GeocodingResult{DisplayName: suggestion.PlacePrediction.Text.Text, FormattedAddress: label})
		if len(results) == limit {
			break
		}
	}
	log.Printf("[GEOCODING] Google search outcome=success results_count=%d duration=%s", len(results), time.Since(started).Round(time.Millisecond))
	return results, nil
}

// decodeGoogle reports body transport faults as retryable and JSON faults as permanent,
// without carrying provider text into the error chain.
func decodeGoogle(body io.Reader, dst any) error {
	reader := &bodyReader{Reader: io.LimitReader(body, googleBodyLimit)}
	err := json.NewDecoder(reader).Decode(dst)
	if reader.Err != nil {
		return newProviderTransportError(reader.Err)
	}
	if err != nil {
		return errors.New("invalid JSON")
	}
	return nil
}

// googleKeyRejected recognises Places' HTTP 400 for a malformed or revoked key
// (status INVALID_ARGUMENT, reason API_KEY_INVALID) without keeping its text.
func googleKeyRejected(resp *http.Response) bool {
	if resp.StatusCode != http.StatusBadRequest {
		return false
	}
	var body struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, googleBodyLimit)).Decode(&body); err != nil {
		return false
	}
	for _, detail := range body.Error.Details {
		if detail.Reason == "API_KEY_INVALID" || detail.Reason == "API_KEY_SERVICE_BLOCKED" {
			return true
		}
	}
	return body.Error.Status == "PERMISSION_DENIED" || body.Error.Status == "UNAUTHENTICATED"
}
