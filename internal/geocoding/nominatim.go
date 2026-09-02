package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"time"
)

// GeocodingResult contains the result of a geocoding operation
type GeocodingResult struct {
	Coords           models.Coordinates
	DisplayName      string
	FormattedAddress string
}

// Label returns the address text shown in search suggestions.
func (r GeocodingResult) Label() string {
	if strings.TrimSpace(r.FormattedAddress) != "" {
		return r.FormattedAddress
	}
	return r.DisplayName
}

// Geocoder provides address-to-coordinates conversion
type Geocoder interface {
	Geocode(ctx context.Context, address string) (*GeocodingResult, error)
	GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error)
	Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error)
}

var ErrNoGeocodingResults = errors.New("geocoding: no results found")

// ErrGeocodingFailed is returned when an address cannot be geocoded
type ErrGeocodingFailed struct {
	Address      string
	Reason       string
	Cause        error
	HTTPStatus   int
	RetryAfter   time.Duration
	ResponseBody string
}

func (e *ErrGeocodingFailed) Error() string {
	return fmt.Sprintf("geocoding failed for address: %s - %s", e.Address, e.Reason)
}

func (e *ErrGeocodingFailed) Unwrap() error {
	return e.Cause
}

type nominatimGeocoder struct {
	baseURL     string
	httpClient  *http.Client
	rateLimiter *time.Ticker
}

type nominatimResponse struct {
	Lat         string           `json:"lat"`
	Lon         string           `json:"lon"`
	DisplayName string           `json:"display_name"`
	Address     nominatimAddress `json:"address"`
	Name        string           `json:"name"`
}

type nominatimAddress struct {
	HouseNumber   string `json:"house_number"`
	Road          string `json:"road"`
	Pedestrian    string `json:"pedestrian"`
	Footway       string `json:"footway"`
	Cycleway      string `json:"cycleway"`
	Path          string `json:"path"`
	Amenity       string `json:"amenity"`
	Building      string `json:"building"`
	House         string `json:"house"`
	Shop          string `json:"shop"`
	Tourism       string `json:"tourism"`
	Leisure       string `json:"leisure"`
	Office        string `json:"office"`
	Suburb        string `json:"suburb"`
	Neighbourhood string `json:"neighbourhood"`
	CityDistrict  string `json:"city_district"`
	Quarter       string `json:"quarter"`
	City          string `json:"city"`
	Town          string `json:"town"`
	Village       string `json:"village"`
	Hamlet        string `json:"hamlet"`
	Municipality  string `json:"municipality"`
	County        string `json:"county"`
	State         string `json:"state"`
	Postcode      string `json:"postcode"`
	Country       string `json:"country"`
	CountryCode   string `json:"country_code"`
}

const (
	geocoderClientTimeout = 10 * time.Second
	nominatimRateInterval = 1 * time.Second
	nominatimMaxAttempts  = 3
	providerBodyLimit     = 4 << 10
)

// NewNominatimGeocoder creates a geocoder using Nominatim
func NewNominatimGeocoder() Geocoder {
	httpClient := &http.Client{
		Timeout: geocoderClientTimeout,
	}

	return newNominatimGeocoder("https://nominatim.openstreetmap.org", httpClient, time.NewTicker(nominatimRateInterval))
}

func newNominatimGeocoder(baseURL string, httpClient *http.Client, rateLimiter *time.Ticker) *nominatimGeocoder {
	return &nominatimGeocoder{
		baseURL:     baseURL,
		httpClient:  httpClient,
		rateLimiter: rateLimiter,
	}
}

func (g *nominatimGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	started := time.Now()
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&addressdetails=1&limit=1", g.baseURL, url.QueryEscape(address))
	log.Printf("[GEOCODING] Nominatim request started")

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Nominatim geocode outcome=request_creation_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "request creation failed", Cause: err}
	}

	req.Header.Set("User-Agent", "RideHomeRouter/1.0 (+https://github.com/aryan-binazir/ride-home-router)")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Nominatim geocode outcome=request_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "provider request failed", Cause: &nominatimTransportError{Cause: err}}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, providerBodyLimit))
		log.Printf("[ERROR] Nominatim geocode outcome=http_error status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{
			Address:      address,
			Reason:       "provider returned an error",
			HTTPStatus:   resp.StatusCode,
			RetryAfter:   parseNominatimRetryAfter(resp.Header.Get("Retry-After")),
			ResponseBody: strings.TrimSpace(string(body)),
		}
	}

	var results []nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[ERROR] Nominatim geocode outcome=decode_failed status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "malformed provider response", Cause: err}
	}

	if len(results) == 0 {
		log.Printf("[GEOCODING] Nominatim geocode outcome=no_results status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "no results found", Cause: ErrNoGeocodingResults}
	}

	result := results[0]
	var lat, lng float64
	if _, err := fmt.Sscanf(result.Lat, "%f", &lat); err != nil {
		log.Printf("[ERROR] Nominatim geocode outcome=invalid_latitude status=%d value=%s duration=%s", resp.StatusCode, logutil.SafeString(result.Lat), time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "invalid latitude"}
	}
	if _, err := fmt.Sscanf(result.Lon, "%f", &lng); err != nil {
		log.Printf("[ERROR] Nominatim geocode outcome=invalid_longitude status=%d value=%s duration=%s", resp.StatusCode, logutil.SafeString(result.Lon), time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: address, Reason: "invalid longitude"}
	}

	log.Printf("[GEOCODING] Nominatim geocode outcome=success status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
	return &GeocodingResult{
		Coords: models.Coordinates{
			Lat: lat,
			Lng: lng,
		},
		DisplayName:      result.DisplayName,
		FormattedAddress: formatAddressLabel(result),
	}, nil
}

func (g *nominatimGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	return geocodeWithRetry(ctx, address, maxRetries, g.Geocode)
}

func (g *nominatimGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	var lastErr error
	for attempt := range nominatimMaxAttempts {
		results, err := g.searchOnce(ctx, query, limit)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		delay, retryable := nominatimRetryDelay(err, attempt)
		if !retryable || attempt == nominatimMaxAttempts-1 {
			return nil, err
		}
		if err := waitForNominatimRetry(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (g *nominatimGeocoder) searchOnce(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	started := time.Now()
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&addressdetails=1&limit=%d", g.baseURL, url.QueryEscape(query), limit)
	log.Printf("[GEOCODING] Nominatim search started: limit=%d", limit)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Nominatim search outcome=request_creation_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: query, Reason: "request creation failed", Cause: err}
	}

	req.Header.Set("User-Agent", "RideHomeRouter/1.0 (+https://github.com/aryan-binazir/ride-home-router)")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Nominatim search outcome=request_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: query, Reason: "provider request failed", Cause: &nominatimTransportError{Cause: err}}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, providerBodyLimit))
		log.Printf("[ERROR] Nominatim search outcome=http_error status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{
			Address:      query,
			Reason:       "provider returned an error",
			HTTPStatus:   resp.StatusCode,
			RetryAfter:   parseNominatimRetryAfter(resp.Header.Get("Retry-After")),
			ResponseBody: strings.TrimSpace(string(body)),
		}
	}

	var results []nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[ERROR] Nominatim search outcome=decode_failed status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &ErrGeocodingFailed{Address: query, Reason: "malformed provider response", Cause: err}
	}

	log.Printf("[GEOCODING] Nominatim search outcome=success status=%d results_count=%d duration=%s", resp.StatusCode, len(results), time.Since(started).Round(time.Millisecond))

	geocodingResults := make([]GeocodingResult, 0, len(results))
	for _, result := range results {
		var lat, lng float64
		if _, err := fmt.Sscanf(result.Lat, "%f", &lat); err != nil {
			log.Printf("[ERROR] Nominatim search result outcome=invalid_latitude value=%s", logutil.SafeString(result.Lat))
			continue
		}
		if _, err := fmt.Sscanf(result.Lon, "%f", &lng); err != nil {
			log.Printf("[ERROR] Nominatim search result outcome=invalid_longitude value=%s", logutil.SafeString(result.Lon))
			continue
		}

		geocodingResults = append(geocodingResults, GeocodingResult{
			Coords: models.Coordinates{
				Lat: lat,
				Lng: lng,
			},
			DisplayName:      result.DisplayName,
			FormattedAddress: formatAddressLabel(result),
		})
	}

	return geocodingResults, nil
}

func formatAddressLabel(result nominatimResponse) string {
	primary := firstNonEmpty(
		joinNonEmpty(" ", result.Address.HouseNumber, firstNonEmpty(
			result.Address.Road,
			result.Address.Pedestrian,
			result.Address.Footway,
			result.Address.Cycleway,
			result.Address.Path,
		)),
		result.Name,
		result.Address.Amenity,
		result.Address.Building,
		result.Address.House,
		result.Address.Shop,
		result.Address.Tourism,
		result.Address.Leisure,
		result.Address.Office,
	)

	locality := firstNonEmpty(
		result.Address.City,
		result.Address.Town,
		result.Address.Village,
		result.Address.Hamlet,
		result.Address.Municipality,
		result.Address.Suburb,
		result.Address.Neighbourhood,
		result.Address.CityDistrict,
		result.Address.Quarter,
		result.Address.County,
	)

	region := strings.TrimSpace(result.Address.State)
	if strings.EqualFold(result.Address.CountryCode, "us") {
		if abbrev, ok := usStateAbbreviations[strings.ToLower(region)]; ok {
			region = abbrev
		}
	}

	regionAndPostcode := joinNonEmpty(" ", region, result.Address.Postcode)
	parts := uniqueNonEmpty(primary, locality, regionAndPostcode)
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}

	return fallbackDisplayName(result.DisplayName)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func joinNonEmpty(sep string, values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, sep)
}

func uniqueNonEmpty(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))

	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		unique = append(unique, trimmed)
	}

	return unique
}

func fallbackDisplayName(displayName string) string {
	parts := strings.Split(displayName, ",")
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			trimmed = append(trimmed, value)
		}
		if len(trimmed) == 4 {
			break
		}
	}

	if len(trimmed) == 0 {
		return strings.TrimSpace(displayName)
	}

	return strings.Join(trimmed, ", ")
}

var usStateAbbreviations = map[string]string{
	"alabama":              "AL",
	"alaska":               "AK",
	"arizona":              "AZ",
	"arkansas":             "AR",
	"california":           "CA",
	"colorado":             "CO",
	"connecticut":          "CT",
	"delaware":             "DE",
	"district of columbia": "DC",
	"florida":              "FL",
	"georgia":              "GA",
	"hawaii":               "HI",
	"idaho":                "ID",
	"illinois":             "IL",
	"indiana":              "IN",
	"iowa":                 "IA",
	"kansas":               "KS",
	"kentucky":             "KY",
	"louisiana":            "LA",
	"maine":                "ME",
	"maryland":             "MD",
	"massachusetts":        "MA",
	"michigan":             "MI",
	"minnesota":            "MN",
	"mississippi":          "MS",
	"missouri":             "MO",
	"montana":              "MT",
	"nebraska":             "NE",
	"nevada":               "NV",
	"new hampshire":        "NH",
	"new jersey":           "NJ",
	"new mexico":           "NM",
	"new york":             "NY",
	"north carolina":       "NC",
	"north dakota":         "ND",
	"ohio":                 "OH",
	"oklahoma":             "OK",
	"oregon":               "OR",
	"pennsylvania":         "PA",
	"rhode island":         "RI",
	"south carolina":       "SC",
	"south dakota":         "SD",
	"tennessee":            "TN",
	"texas":                "TX",
	"utah":                 "UT",
	"vermont":              "VT",
	"virginia":             "VA",
	"washington":           "WA",
	"west virginia":        "WV",
	"wisconsin":            "WI",
	"wyoming":              "WY",
}

func geocodeWithRetry(ctx context.Context, address string, maxRetries int, geocode func(context.Context, string) (*GeocodingResult, error)) (*GeocodingResult, error) {
	var lastErr error
	started := time.Now()
	attempts := max(1, min(maxRetries, nominatimMaxAttempts))

	for i := range attempts {
		result, err := geocode(ctx, address)
		if err == nil {
			log.Printf("[GEOCODING] Retry operation outcome=success attempts=%d duration=%s", i+1, time.Since(started).Round(time.Millisecond))
			return result, nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		backoff, retryable := nominatimRetryDelay(err, i)
		if !retryable {
			break
		}
		if i < attempts-1 {
			log.Printf("[GEOCODING] Retry %d/%d: backoff=%v", i+1, maxRetries, backoff)
			if err := waitForNominatimRetry(ctx, backoff); err != nil {
				return nil, err
			}
		}
	}

	log.Printf("[ERROR] Retry operation outcome=failed retries=%d duration=%s", maxRetries, time.Since(started).Round(time.Millisecond))
	return nil, lastErr
}

type nominatimTransportError struct {
	Cause error
}

func (e *nominatimTransportError) Error() string {
	return e.Cause.Error()
}

func (e *nominatimTransportError) Unwrap() error {
	return e.Cause
}

func nominatimRetryDelay(err error, attempt int) (time.Duration, bool) {
	var geocodingErr *ErrGeocodingFailed
	if !errors.As(err, &geocodingErr) {
		return 0, false
	}

	retryable := false
	if _, ok := errors.AsType[*nominatimTransportError](geocodingErr.Cause); ok {
		retryable = true
	} else {
		retryable = isNominatimRetryableStatus(geocodingErr.HTTPStatus)
	}
	if !retryable {
		return 0, false
	}

	base := time.Second << attempt
	//nolint:gosec // G404: retry jitter does not need cryptographic randomness.
	delay := max(base+time.Duration(rand.Int64N(int64(base/2)+1)), geocodingErr.RetryAfter)
	return max(delay, time.Second), true
}

func isNominatimRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitForNominatimRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseNominatimRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return max(time.Until(when), 0)
}
