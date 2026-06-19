package distance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strings"
	"time"
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
	if _, err := c.currentAPIKey(); err != nil {
		return nil, err
	}

	cached, err := c.cache.Get(ctx, origin, dest)
	if err != nil && !errors.Is(err, database.ErrCacheMiss) {
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
	if matrixNeedsProvider(points) {
		if _, err := c.currentAPIKey(); err != nil {
			return nil, err
		}
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
	}

	missingPairs, err := c.hydrateMatrixFromCache(ctx, points, matrix)
	if err != nil {
		return nil, err
	}
	if len(missingPairs) == 0 {
		return matrix, nil
	}

	var cacheEntries []models.DistanceCacheEntry
	for destStart := 0; destStart < n; {
		destEnd := min(destStart+googleRouteMatrixMaxElements, n)
		destCount := destEnd - destStart
		maxOrigins := max(1, googleRouteMatrixMaxElements/destCount)

		for originStart := 0; originStart < n; originStart += maxOrigins {
			originEnd := min(originStart+maxOrigins, n)
			sourceIndexes, destIndexes := collectGoogleMatrixBlock(originStart, originEnd, destStart, destEnd, missingPairs)
			if len(sourceIndexes) == 0 || len(destIndexes) == 0 {
				continue
			}

			results, err := c.fetchMatrix(ctx, coordinatesForIndexes(points, sourceIndexes), coordinatesForIndexes(points, destIndexes))
			if err != nil {
				return nil, err
			}

			for localSourceIndex, sourceIndex := range sourceIndexes {
				for localDestIndex, destIndex := range destIndexes {
					pair := matrixIndex{origin: sourceIndex, destination: destIndex}
					if _, ok := missingPairs[pair]; !ok {
						continue
					}

					result, ok := results[matrixIndex{origin: localSourceIndex, destination: localDestIndex}]
					if !ok {
						return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response missing element"}
					}
					matrix[sourceIndex][destIndex] = result
					cacheEntries = append(cacheEntries, models.DistanceCacheEntry{
						Origin:         points[sourceIndex],
						Destination:    points[destIndex],
						DistanceMeters: result.DistanceMeters,
						DurationSecs:   result.DurationSecs,
					})
				}
			}
		}
		destStart = destEnd
	}

	if err := c.cache.SetBatch(ctx, cacheEntries); err != nil {
		return nil, err
	}

	return matrix, nil
}

func (c *googleCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error) {
	if len(destinations) == 0 {
		return []DistanceResult{}, nil
	}
	if destinationsNeedProvider(origin, destinations) {
		if _, err := c.currentAPIKey(); err != nil {
			return nil, err
		}
	}

	results := make([]DistanceResult, len(destinations))
	var missingDestinations []models.Coordinates
	var missingIndexes []int
	for i, dest := range destinations {
		if sameRoundedPoint(origin, dest) {
			continue
		}

		cached, err := c.cache.Get(ctx, origin, dest)
		if err != nil && !errors.Is(err, database.ErrCacheMiss) {
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

func (c *googleCalculator) hydrateMatrixFromCache(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult) (map[matrixIndex]struct{}, error) {
	var cachePairs []struct{ Origin, Dest models.Coordinates }
	var indexes []matrixIndex
	for originIndex, origin := range points {
		for destIndex, dest := range points {
			if originIndex == destIndex || sameRoundedPoint(origin, dest) {
				continue
			}
			cachePairs = append(cachePairs, struct{ Origin, Dest models.Coordinates }{Origin: origin, Dest: dest})
			indexes = append(indexes, matrixIndex{origin: originIndex, destination: destIndex})
		}
	}

	cached, err := c.cache.GetBatch(ctx, cachePairs)
	if err != nil {
		return nil, err
	}

	missing := make(map[matrixIndex]struct{})
	for i, pair := range cachePairs {
		index := indexes[i]
		entry := cached[googleCacheKey(pair.Origin, pair.Dest)]
		if entry == nil {
			missing[index] = struct{}{}
			continue
		}
		matrix[index.origin][index.destination] = DistanceResult{
			DistanceMeters: entry.DistanceMeters,
			DurationSecs:   entry.DurationSecs,
		}
	}
	return missing, nil
}

func collectGoogleMatrixBlock(originStart, originEnd, destStart, destEnd int, missingPairs map[matrixIndex]struct{}) ([]int, []int) {
	sourceSeen := make(map[int]struct{}, originEnd-originStart)
	destSeen := make(map[int]struct{}, destEnd-destStart)
	var sourceIndexes []int
	var destIndexes []int

	for originIndex := originStart; originIndex < originEnd; originIndex++ {
		for destIndex := destStart; destIndex < destEnd; destIndex++ {
			if _, ok := missingPairs[matrixIndex{origin: originIndex, destination: destIndex}]; !ok {
				continue
			}
			if _, ok := sourceSeen[originIndex]; !ok {
				sourceSeen[originIndex] = struct{}{}
				sourceIndexes = append(sourceIndexes, originIndex)
			}
			if _, ok := destSeen[destIndex]; !ok {
				destSeen[destIndex] = struct{}{}
				destIndexes = append(destIndexes, destIndex)
			}
		}
	}

	return sourceIndexes, destIndexes
}

func coordinatesForIndexes(points []models.Coordinates, indexes []int) []models.Coordinates {
	coordinates := make([]models.Coordinates, len(indexes))
	for i, index := range indexes {
		coordinates[i] = points[index]
	}
	return coordinates
}

func googleCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat),
		models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat),
		models.RoundCoordinate(dest.Lng),
	)
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
		}
		return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("Google route matrix HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))}
	}

	elements, err := parseGoogleMatrixElements(resp.Body)
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
		return "", fmt.Errorf("%w: Google Maps API key is missing; add it in Settings", ErrProviderNotConfigured)
	}
	return apiKey, nil
}

func sameRoundedPoint(a, b models.Coordinates) bool {
	return models.RoundCoordinate(a.Lat) == models.RoundCoordinate(b.Lat) &&
		models.RoundCoordinate(a.Lng) == models.RoundCoordinate(b.Lng)
}

func matrixNeedsProvider(points []models.Coordinates) bool {
	for i, origin := range points {
		for j, dest := range points {
			if i != j && !sameRoundedPoint(origin, dest) {
				return true
			}
		}
	}
	return false
}

func destinationsNeedProvider(origin models.Coordinates, destinations []models.Coordinates) bool {
	for _, dest := range destinations {
		if !sameRoundedPoint(origin, dest) {
			return true
		}
	}
	return false
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

func parseGoogleMatrixElements(body io.Reader) ([]googleMatrixElement, error) {
	reader := bufio.NewReader(body)
	first, err := peekFirstNonSpace(reader)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response was empty"}
		}
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	decoder := json.NewDecoder(reader)
	if first == '[' {
		var elements []googleMatrixElement
		if err := decoder.Decode(&elements); err != nil {
			return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
		}
		return elements, nil
	}

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

func peekFirstNonSpace(reader *bufio.Reader) (byte, error) {
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if err := reader.UnreadByte(); err != nil {
			return 0, err
		}
		return b, nil
	}
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
