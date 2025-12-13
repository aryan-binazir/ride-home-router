package geocoding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"ride-home-router/internal/models"
)

// GeocodingResult contains the result of a geocoding operation
type GeocodingResult struct {
	Coords      models.Coordinates
	DisplayName string
}

// Geocoder provides address-to-coordinates conversion
type Geocoder interface {
	Geocode(ctx context.Context, address string) (*GeocodingResult, error)
	GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error)
	Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error)
}

// ErrGeocodingFailed is returned when an address cannot be geocoded
type ErrGeocodingFailed struct {
	Address string
	Reason  string
}

func (e *ErrGeocodingFailed) Error() string {
	return fmt.Sprintf("geocoding failed for address: %s - %s", e.Address, e.Reason)
}

type nominatimGeocoder struct {
	baseURL    string
	httpClient *http.Client
	rateLimiter *time.Ticker
}

type nominatimResponse struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

// NewNominatimGeocoder creates a new Nominatim geocoder with rate limiting
func NewNominatimGeocoder() Geocoder {
	return &nominatimGeocoder{
		baseURL: "https://nominatim.openstreetmap.org",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		rateLimiter: time.NewTicker(1 * time.Second),
	}
}

func (g *nominatimGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&limit=1", g.baseURL, url.QueryEscape(address))
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
		return nil, &ErrGeocodingFailed{Address: address, Reason: "no results found"}
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
		DisplayName: result.DisplayName,
	}, nil
}

func (g *nominatimGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		result, err := g.Geocode(ctx, address)
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

func (g *nominatimGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	select {
	case <-g.rateLimiter.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	queryURL := fmt.Sprintf("%s/search?q=%s&format=json&limit=%d", g.baseURL, url.QueryEscape(query), limit)
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
			DisplayName: result.DisplayName,
		})
	}

	return geocodingResults, nil
}
