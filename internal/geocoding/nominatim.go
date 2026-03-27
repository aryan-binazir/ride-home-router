package geocoding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"ride-home-router/internal/models"
)

// GeocodingResult contains the result of a geocoding operation
type GeocodingResult struct {
	Coords           models.Coordinates
	DisplayName      string
	FormattedAddress string
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
	Address string
	Reason  string
	Cause   error
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

// NewNominatimGeocoder creates a geocoder using Nominatim as primary with Census as fallback
func NewNominatimGeocoder() Geocoder {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	return &fallbackGeocoder{
		primary:  newNominatimGeocoder("https://nominatim.openstreetmap.org", httpClient, time.NewTicker(1*time.Second)),
		fallback: newCensusGeocoder("https://geocoding.geo.census.gov", httpClient),
	}
}

func newNominatimGeocoder(baseURL string, httpClient *http.Client, rateLimiter *time.Ticker) *nominatimGeocoder {
	return &nominatimGeocoder{
		baseURL:     baseURL,
		httpClient:  httpClient,
		rateLimiter: rateLimiter,
	}
}

func (g *nominatimGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&addressdetails=1&limit=1", g.baseURL, url.QueryEscape(address))
	log.Printf("[GEOCODING] Request: address=%s url=%s", address, queryURL)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Failed to create geocoding request: address=%s err=%v", address, err)
		return nil, &ErrGeocodingFailed{Address: address, Reason: err.Error()}
	}

	req.Header.Set("User-Agent", "RideHomeRouter/1.0")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Geocoding API request failed: address=%s err=%v", address, err)
		return nil, &ErrGeocodingFailed{Address: address, Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] Geocoding API error: address=%s status=%d body=%s", address, resp.StatusCode, string(body))
		return nil, &ErrGeocodingFailed{
			Address: address,
			Reason:  fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var results []nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[ERROR] Failed to decode geocoding response: address=%s err=%v", address, err)
		return nil, &ErrGeocodingFailed{Address: address, Reason: err.Error()}
	}

	if len(results) == 0 {
		log.Printf("[ERROR] No geocoding results found: address=%s", address)
		return nil, &ErrGeocodingFailed{Address: address, Reason: "no results found", Cause: ErrNoGeocodingResults}
	}

	result := results[0]
	var lat, lng float64
	if _, err := fmt.Sscanf(result.Lat, "%f", &lat); err != nil {
		log.Printf("[ERROR] Invalid latitude in geocoding response: address=%s lat=%s err=%v", address, result.Lat, err)
		return nil, &ErrGeocodingFailed{Address: address, Reason: "invalid latitude"}
	}
	if _, err := fmt.Sscanf(result.Lon, "%f", &lng); err != nil {
		log.Printf("[ERROR] Invalid longitude in geocoding response: address=%s lng=%s err=%v", address, result.Lon, err)
		return nil, &ErrGeocodingFailed{Address: address, Reason: "invalid longitude"}
	}

	log.Printf("[GEOCODING] Response: address=%s lat=%.6f lng=%.6f display_name=%s", address, lat, lng, result.DisplayName)
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
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&addressdetails=1&limit=%d", g.baseURL, url.QueryEscape(query), limit)
	log.Printf("[GEOCODING] Search request: query=%s limit=%d url=%s", query, limit, queryURL)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Failed to create geocoding search request: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}

	req.Header.Set("User-Agent", "RideHomeRouter/1.0")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Geocoding search API request failed: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] Geocoding search API error: query=%s status=%d body=%s", query, resp.StatusCode, string(body))
		return nil, &ErrGeocodingFailed{
			Address: query,
			Reason:  fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var results []nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[ERROR] Failed to decode geocoding search response: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}

	log.Printf("[GEOCODING] Search response: query=%s results_count=%d", query, len(results))

	geocodingResults := make([]GeocodingResult, 0, len(results))
	for _, result := range results {
		var lat, lng float64
		if _, err := fmt.Sscanf(result.Lat, "%f", &lat); err != nil {
			log.Printf("[ERROR] Invalid latitude in geocoding search response: query=%s lat=%s err=%v", query, result.Lat, err)
			continue
		}
		if _, err := fmt.Sscanf(result.Lon, "%f", &lng); err != nil {
			log.Printf("[ERROR] Invalid longitude in geocoding search response: query=%s lng=%s err=%v", query, result.Lon, err)
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

var usStateCodePattern = regexp.MustCompile(`\b(?:AL|AK|AZ|AR|CA|CO|CT|DE|DC|FL|GA|HI|ID|IL|IN|IA|KS|KY|LA|ME|MD|MA|MI|MN|MS|MO|MT|NE|NV|NH|NJ|NM|NY|NC|ND|OH|OK|OR|PA|RI|SC|SD|TN|TX|UT|VT|VA|WA|WV|WI|WY)\b`)
var usZIPCodePattern = regexp.MustCompile(`\b\d{5}(?:-\d{4})?\b`)
var addressNumberPattern = regexp.MustCompile(`\d`)

func geocodeWithRetry(ctx context.Context, address string, maxRetries int, geocode func(context.Context, string) (*GeocodingResult, error)) (*GeocodingResult, error) {
	var lastErr error

	for i := range maxRetries {
		result, err := geocode(ctx, address)
		if err == nil {
			log.Printf("[GEOCODING] Success after %d attempt(s): address=%s", i+1, address)
			return result, nil
		}

		lastErr = err

		if i < maxRetries-1 {
			backoff := time.Duration(1<<uint(i)) * time.Second
			log.Printf("[GEOCODING] Retry %d/%d: address=%s backoff=%v err=%v", i+1, maxRetries, address, backoff, err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	log.Printf("[ERROR] Geocoding failed after %d retries: address=%s err=%v", maxRetries, address, lastErr)
	return nil, lastErr
}

func isNoResultsError(err error) bool {
	return errors.Is(err, ErrNoGeocodingResults)
}

func looksLikeUSAddressQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || !addressNumberPattern.MatchString(trimmed) {
		return false
	}

	normalized := strings.ToUpper(strings.NewReplacer(",", " ", ".", " ").Replace(trimmed))
	return usZIPCodePattern.MatchString(normalized) || usStateCodePattern.MatchString(normalized)
}

func isSpecificUSAddressQuery(query string) bool {
	trimmed := strings.TrimSpace(query)
	if !looksLikeUSAddressQuery(trimmed) {
		return false
	}

	return usZIPCodePattern.MatchString(trimmed) || strings.Count(trimmed, ",") >= 2
}
