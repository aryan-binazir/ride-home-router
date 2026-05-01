package distance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

const (
	googleRouteMatrixURL         = "https://routes.googleapis.com/distanceMatrix/v2:computeRouteMatrix"
	googleRouteMatrixFieldMask   = "originIndex,destinationIndex,status,condition,distanceMeters,duration"
	googleRouteMatrixMaxElements = 625
	googleHTTPTimeout            = 60 * time.Second
)

var ErrProviderNotConfigured = errors.New("distance provider is not configured")

type APIKeyProvider func() (string, error)

type googleCalculator struct {
	httpClient *http.Client
	cache      database.DistanceCacheRepository
	apiKey     APIKeyProvider
	endpoint   string
}

func NewGoogleCalculator(cache database.DistanceCacheRepository, apiKey APIKeyProvider) DistanceCalculator {
	return &googleCalculator{
		httpClient: &http.Client{Timeout: googleHTTPTimeout},
		cache:      cache,
		apiKey:     apiKey,
		endpoint:   googleRouteMatrixURL,
	}
}

func (c *googleCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error) {
	if sameRoundedPoint(origin, dest) {
		return &DistanceResult{DistanceMeters: 0, DurationSecs: 0}, nil
	}

	cached, err := c.cache.Get(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		return &DistanceResult{
			DistanceMeters: cached.DistanceMeters,
			DurationSecs:   cached.DurationSecs,
		}, nil
	}

	results, err := c.GetDistancesFromPoint(ctx, origin, []models.Coordinates{dest})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, &ErrDistanceCalculationFailed{Origin: origin, Dest: dest, Reason: "no results returned"}
	}
	return &results[0], nil
}

func (c *googleCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error) {
	n := len(points)
	if n == 0 {
		return [][]DistanceResult{}, nil
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
	}

	for originIndex, origin := range points {
		var missingDestinations []models.Coordinates
		var missingIndexes []int
		for destIndex, dest := range points {
			if originIndex == destIndex || sameRoundedPoint(origin, dest) {
				continue
			}

			cached, err := c.cache.Get(ctx, origin, dest)
			if err != nil {
				return nil, err
			}
			if cached != nil {
				matrix[originIndex][destIndex] = DistanceResult{
					DistanceMeters: cached.DistanceMeters,
					DurationSecs:   cached.DurationSecs,
				}
				continue
			}

			missingDestinations = append(missingDestinations, dest)
			missingIndexes = append(missingIndexes, destIndex)
		}

		for start := 0; start < len(missingDestinations); start += googleRouteMatrixMaxElements {
			end := min(start+googleRouteMatrixMaxElements, len(missingDestinations))
			chunkDestinations := missingDestinations[start:end]
			chunkIndexes := missingIndexes[start:end]

			results, err := c.fetchMatrix(ctx, []models.Coordinates{origin}, chunkDestinations)
			if err != nil {
				return nil, err
			}

			cacheEntries := make([]models.DistanceCacheEntry, 0, len(chunkDestinations))
			for localDestIndex, destIndex := range chunkIndexes {
				result, ok := results[matrixIndex{origin: 0, destination: localDestIndex}]
				if !ok {
					return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response missing element"}
				}
				matrix[originIndex][destIndex] = result
				cacheEntries = append(cacheEntries, models.DistanceCacheEntry{
					Origin:         origin,
					Destination:    points[destIndex],
					DistanceMeters: result.DistanceMeters,
					DurationSecs:   result.DurationSecs,
				})
			}
			if err := c.cache.SetBatch(ctx, cacheEntries); err != nil {
				return nil, err
			}
		}
	}

	return matrix, nil
}

func (c *googleCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error) {
	if len(destinations) == 0 {
		return []DistanceResult{}, nil
	}

	results := make([]DistanceResult, len(destinations))
	var missingDestinations []models.Coordinates
	var missingIndexes []int
	for i, dest := range destinations {
		if sameRoundedPoint(origin, dest) {
			continue
		}

		cached, err := c.cache.Get(ctx, origin, dest)
		if err != nil {
			return nil, err
		}
		if cached != nil {
			results[i] = DistanceResult{
				DistanceMeters: cached.DistanceMeters,
				DurationSecs:   cached.DurationSecs,
			}
			continue
		}
		missingDestinations = append(missingDestinations, dest)
		missingIndexes = append(missingIndexes, i)
	}

	for start := 0; start < len(missingDestinations); start += googleRouteMatrixMaxElements {
		end := min(start+googleRouteMatrixMaxElements, len(missingDestinations))
		chunkDestinations := missingDestinations[start:end]
		chunkIndexes := missingIndexes[start:end]

		matrixResults, err := c.fetchMatrix(ctx, []models.Coordinates{origin}, chunkDestinations)
		if err != nil {
			return nil, err
		}

		cacheEntries := make([]models.DistanceCacheEntry, 0, len(chunkDestinations))
		for localDestIndex, resultIndex := range chunkIndexes {
			result, ok := matrixResults[matrixIndex{origin: 0, destination: localDestIndex}]
			if !ok {
				return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response missing element"}
			}
			results[resultIndex] = result
			cacheEntries = append(cacheEntries, models.DistanceCacheEntry{
				Origin:         origin,
				Destination:    destinations[resultIndex],
				DistanceMeters: result.DistanceMeters,
				DurationSecs:   result.DurationSecs,
			})
		}
		if err := c.cache.SetBatch(ctx, cacheEntries); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func (c *googleCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	_, err := c.GetDistanceMatrix(ctx, points)
	return err
}

type matrixIndex struct {
	origin      int
	destination int
}

func (c *googleCalculator) fetchMatrix(ctx context.Context, origins, destinations []models.Coordinates) (map[matrixIndex]DistanceResult, error) {
	if len(origins) == 0 || len(destinations) == 0 {
		return map[matrixIndex]DistanceResult{}, nil
	}
	if len(origins)*len(destinations) > googleRouteMatrixMaxElements {
		return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix batch exceeds 625 elements"}
	}

	apiKey, err := c.currentAPIKey()
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(googleMatrixRequest{
		Origins:           makeGoogleOrigins(origins),
		Destinations:      makeGoogleDestinations(destinations),
		TravelMode:        "DRIVE",
		RoutingPreference: "TRAFFIC_UNAWARE",
	})
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	req.Header.Set("X-Goog-FieldMask", googleRouteMatrixFieldMask)

	log.Printf("[GOOGLE] Route matrix request: origins=%d destinations=%d elements=%d", len(origins), len(destinations), len(origins)*len(destinations))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("Google route matrix HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
	}

	elements, err := parseGoogleMatrixElements(responseBody)
	if err != nil {
		return nil, err
	}

	results := make(map[matrixIndex]DistanceResult, len(elements))
	for _, element := range elements {
		if element.OriginIndex < 0 || element.OriginIndex >= len(origins) || element.DestinationIndex < 0 || element.DestinationIndex >= len(destinations) {
			return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response contained out-of-range index"}
		}
		if element.Status.Code != 0 {
			reason := element.Status.Message
			if reason == "" {
				reason = fmt.Sprintf("Google route matrix element status code %d", element.Status.Code)
			}
			return nil, &ErrDistanceCalculationFailed{Reason: reason}
		}
		if element.Condition != "" && element.Condition != "ROUTE_EXISTS" {
			return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("Google route matrix element condition: %s", element.Condition)}
		}

		durationSecs, err := parseGoogleDurationSeconds(element.Duration)
		if err != nil {
			return nil, err
		}
		results[matrixIndex{origin: element.OriginIndex, destination: element.DestinationIndex}] = DistanceResult{
			DistanceMeters: float64(element.DistanceMeters),
			DurationSecs:   durationSecs,
		}
	}

	return results, nil
}

func (c *googleCalculator) currentAPIKey() (string, error) {
	if c.apiKey == nil {
		return "", fmt.Errorf("%w: Google Maps API key is missing", ErrProviderNotConfigured)
	}
	apiKey, err := c.apiKey()
	if err != nil {
		return "", err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("%w: Google Maps API key is missing. Add it in Settings.", ErrProviderNotConfigured)
	}
	return apiKey, nil
}

func sameRoundedPoint(a, b models.Coordinates) bool {
	return models.RoundCoordinate(a.Lat) == models.RoundCoordinate(b.Lat) &&
		models.RoundCoordinate(a.Lng) == models.RoundCoordinate(b.Lng)
}

type googleMatrixRequest struct {
	Origins           []googleRouteMatrixOrigin      `json:"origins"`
	Destinations      []googleRouteMatrixDestination `json:"destinations"`
	TravelMode        string                         `json:"travelMode"`
	RoutingPreference string                         `json:"routingPreference"`
}

type googleRouteMatrixOrigin struct {
	Waypoint googleWaypoint `json:"waypoint"`
}

type googleRouteMatrixDestination struct {
	Waypoint googleWaypoint `json:"waypoint"`
}

type googleWaypoint struct {
	Location googleLocation `json:"location"`
}

type googleLocation struct {
	LatLng googleLatLng `json:"latLng"`
}

type googleLatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func makeGoogleOrigins(points []models.Coordinates) []googleRouteMatrixOrigin {
	origins := make([]googleRouteMatrixOrigin, len(points))
	for i, point := range points {
		origins[i] = googleRouteMatrixOrigin{Waypoint: makeGoogleWaypoint(point)}
	}
	return origins
}

func makeGoogleDestinations(points []models.Coordinates) []googleRouteMatrixDestination {
	destinations := make([]googleRouteMatrixDestination, len(points))
	for i, point := range points {
		destinations[i] = googleRouteMatrixDestination{Waypoint: makeGoogleWaypoint(point)}
	}
	return destinations
}

func makeGoogleWaypoint(point models.Coordinates) googleWaypoint {
	return googleWaypoint{
		Location: googleLocation{
			LatLng: googleLatLng{
				Latitude:  point.Lat,
				Longitude: point.Lng,
			},
		},
	}
}

type googleMatrixElement struct {
	OriginIndex      int          `json:"originIndex"`
	DestinationIndex int          `json:"destinationIndex"`
	Status           googleStatus `json:"status"`
	Condition        string       `json:"condition"`
	DistanceMeters   int64        `json:"distanceMeters"`
	Duration         string       `json:"duration"`
}

type googleStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func parseGoogleMatrixElements(body []byte) ([]googleMatrixElement, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response was empty"}
	}

	if trimmed[0] == '[' {
		var elements []googleMatrixElement
		if err := json.Unmarshal(trimmed, &elements); err != nil {
			return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
		}
		return elements, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var elements []googleMatrixElement
	for {
		var element googleMatrixElement
		if err := decoder.Decode(&element); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
		}
		elements = append(elements, element)
	}
	return elements, nil
}

func parseGoogleDurationSeconds(value string) (float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, &ErrDistanceCalculationFailed{Reason: "Google route matrix response missing duration"}
	}
	if !strings.HasSuffix(value, "s") {
		return 0, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("invalid Google duration %q", value)}
	}
	seconds, err := time.ParseDuration(value)
	if err != nil {
		return 0, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	return math.Round(seconds.Seconds()*1000) / 1000, nil
}
