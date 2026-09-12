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
	"math/rand/v2"
	"net"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	googleRouteMatrixURL         = "https://routes.googleapis.com/distanceMatrix/v2:computeRouteMatrix"
	googleRouteMatrixFieldMask   = "originIndex,destinationIndex,status,condition,distanceMeters,duration"
	googleRouteMatrixMaxElements = 625
	googleHTTPTimeout            = 60 * time.Second
	googleMaxAttempts            = 3
	googleRetryBaseDelay         = 250 * time.Millisecond
	providerErrorBodyLimit       = 4 << 10
	maxGoogleRetryAfter          = time.Duration(1<<63 - 1)
)

var ErrProviderNotConfigured = errors.New("distance provider is not configured")

type APIKeyProvider func(context.Context) (string, error)

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
	if SamePoint(origin, dest) {
		return &DistanceResult{DistanceMeters: 0, DurationSecs: 0}, nil
	}
	if _, err := c.currentAPIKey(ctx); err != nil {
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
		if _, err := c.currentAPIKey(ctx); err != nil {
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
		if _, err := c.currentAPIKey(ctx); err != nil {
			return nil, err
		}
	}

	results := make([]DistanceResult, len(destinations))
	cachePairs := make([]struct{ Origin, Dest models.Coordinates }, 0, len(destinations))
	cacheIndexes := make([]int, 0, len(destinations))
	for i, dest := range destinations {
		if SamePoint(origin, dest) {
			continue
		}
		cachePairs = append(cachePairs, struct{ Origin, Dest models.Coordinates }{Origin: origin, Dest: dest})
		cacheIndexes = append(cacheIndexes, i)
	}

	cached, err := c.cache.GetBatch(ctx, cachePairs)
	if err != nil {
		return nil, err
	}

	var missingDestinations []models.Coordinates
	var missingIndexes []int
	for pairIndex, pair := range cachePairs {
		resultIndex := cacheIndexes[pairIndex]
		entry := cached[PairCacheKey(pair.Origin, pair.Dest)]
		if entry != nil {
			results[resultIndex] = DistanceResult{
				DistanceMeters: entry.DistanceMeters,
				DurationSecs:   entry.DurationSecs,
			}
			continue
		}
		missingDestinations = append(missingDestinations, pair.Dest)
		missingIndexes = append(missingIndexes, resultIndex)
	}

	if len(missingDestinations) > 0 {
		if _, err := c.currentAPIKey(ctx); err != nil {
			return nil, err
		}
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

// MaxUncachedDistancePairs bounds the elements one calculation can request.
const MaxUncachedDistancePairs = 60000

var (
	//nolint:staticcheck // This sentinel is safe consumer-facing fallback copy.
	ErrTooManyDistancePairs = errors.New("Too many riders and drivers selected for one calculation. Select fewer and try again.")
	providerCalculations    = make(chan struct{}, 4)
)

type prewarmBlock struct{ origins, destinations []models.Coordinates }

func (c *googleCalculator) PrewarmPairs(ctx context.Context, pairs []DistancePair) error {
	if len(pairs) == 0 {
		return nil
	}
	cachePairs := make([]struct{ Origin, Dest models.Coordinates }, 0, len(pairs))
	seen := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := PairCacheKey(pair.Origin, pair.Destination)
		if SamePoint(pair.Origin, pair.Destination) || seen[key] {
			continue
		}
		seen[key] = true
		cachePairs = append(cachePairs, struct{ Origin, Dest models.Coordinates }{pair.Origin, pair.Destination})
	}
	var missing map[string]bool
	var byOrigin map[string][]models.Coordinates
	var origins map[string]models.Coordinates
	hydrate := func() error {
		cached, err := c.cache.GetBatch(ctx, cachePairs)
		if err != nil {
			return err
		}
		missing = make(map[string]bool)
		byOrigin = make(map[string][]models.Coordinates)
		origins = make(map[string]models.Coordinates)
		for _, pair := range cachePairs {
			key := PairCacheKey(pair.Origin, pair.Dest)
			if cached[key] != nil {
				continue
			}
			missing[key] = true
			originKey := coordinatePointKey(pair.Origin)
			byOrigin[originKey] = append(byOrigin[originKey], pair.Dest)
			origins[originKey] = pair.Origin
		}
		if len(missing) > MaxUncachedDistancePairs {
			return ErrTooManyDistancePairs
		}
		return nil
	}
	if err := hydrate(); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	select {
	case providerCalculations <- struct{}{}:
		defer func() { <-providerCalculations }()
	case <-ctx.Done():
		return ctx.Err()
	}
	// Calculations ahead of us may have filled these entries while we waited.
	if err := hydrate(); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	keys := make([]string, 0, len(byOrigin))
	for key := range byOrigin {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	blocks := []prewarmBlock{}
	for _, key := range keys {
		destinations := byOrigin[key]
		sort.Slice(destinations, func(i, j int) bool { return coordinatePointKey(destinations[i]) < coordinatePointKey(destinations[j]) })
		for start := 0; start < len(destinations); start += googleRouteMatrixMaxElements {
			chunk := destinations[start:min(start+googleRouteMatrixMaxElements, len(destinations))]
			merged := false
			if len(blocks) > 0 {
				block := &blocks[len(blocks)-1]
				union := uniqueCoordinates(append(append([]models.Coordinates{}, block.destinations...), chunk...))
				candidates := append(append([]models.Coordinates{}, block.origins...), origins[key])
				compatible := len(candidates)*len(union) <= googleRouteMatrixMaxElements
				for _, origin := range candidates {
					for _, dest := range union {
						if !SamePoint(origin, dest) && !missing[PairCacheKey(origin, dest)] {
							compatible = false
							break
						}
					}
					if !compatible {
						break
					}
				}
				if compatible {
					block.origins = candidates
					block.destinations = union
					merged = true
				}
			}
			if !merged {
				blocks = append(blocks, prewarmBlock{origins: []models.Coordinates{origins[key]}, destinations: chunk})
			}
		}
	}
	// Matrix self-elements can be unavoidable when packing almost identical rows.
	// Count them too, so billed elements remain bounded by the same ceiling.
	billed := 0
	for _, block := range blocks {
		billed += len(block.origins) * len(block.destinations)
	}
	if billed > MaxUncachedDistancePairs {
		blocks = nil
		for _, key := range keys {
			destinations := byOrigin[key]
			for start := 0; start < len(destinations); start += googleRouteMatrixMaxElements {
				blocks = append(blocks, prewarmBlock{origins: []models.Coordinates{origins[key]}, destinations: destinations[start:min(start+googleRouteMatrixMaxElements, len(destinations))]})
			}
		}
	}
	workCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	failures := make([]error, len(blocks))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(blocks)) {
		workers.Go(func() {
			for index := range jobs {
				if workCtx.Err() != nil {
					return
				}
				block := blocks[index]
				results, err := c.fetchMatrix(workCtx, block.origins, block.destinations)
				if err == nil {
					entries := make([]models.DistanceCacheEntry, 0, len(results))
					for i, origin := range block.origins {
						for j, dest := range block.destinations {
							if SamePoint(origin, dest) {
								continue
							}
							result, ok := results[matrixIndex{origin: i, destination: j}]
							if !ok {
								err = &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again."}
								break
							}
							entries = append(entries, models.DistanceCacheEntry{Origin: origin, Destination: dest, DistanceMeters: result.DistanceMeters, DurationSecs: result.DurationSecs})
						}
						if err != nil {
							break
						}
					}
					if err == nil {
						err = c.cache.SetBatch(workCtx, entries)
					}
				}
				failures[index] = err
				if err != nil && !isGoogleRetryableFailure(err) {
					cancel(err)
					return
				}
			}
		})
	}
dispatch:
	for index := range blocks {
		select {
		case jobs <- index:
		case <-workCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if cause := context.Cause(workCtx); cause != nil {
		return cause
	}
	for _, err := range failures {
		if err != nil {
			return err
		}
	}
	return nil
}

func uniqueCoordinates(points []models.Coordinates) []models.Coordinates {
	seen := make(map[string]struct{}, len(points))
	unique := make([]models.Coordinates, 0, len(points))
	for _, point := range points {
		key := coordinatePointKey(point)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, point)
	}
	return unique
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
			if originIndex == destIndex || SamePoint(origin, dest) {
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
		entry := cached[PairCacheKey(pair.Origin, pair.Dest)]
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

func (c *googleCalculator) fetchMatrix(ctx context.Context, origins, destinations []models.Coordinates) (map[matrixIndex]DistanceResult, error) {
	if len(origins) == 0 || len(destinations) == 0 {
		return map[matrixIndex]DistanceResult{}, nil
	}
	if len(origins)*len(destinations) > googleRouteMatrixMaxElements {
		return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix batch exceeds 625 elements"}
	}

	for attempt := range googleMaxAttempts {
		results, err := c.fetchMatrixOnce(ctx, origins, destinations)
		if err == nil {
			return results, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		delay, retryable := googleRetryDelay(ctx, err, attempt)
		if !retryable {
			return nil, googleMatrixPublicError(err)
		}
		if attempt == googleMaxAttempts-1 {
			return nil, &googleRetryError{googleMatrixError: googleMatrixPublicError(err)}
		}
		if err := waitForGoogleRetry(ctx, delay); err != nil {
			return nil, err
		}
	}

	panic("unreachable")
}

func (c *googleCalculator) fetchMatrixOnce(ctx context.Context, origins, destinations []models.Coordinates) (_ map[matrixIndex]DistanceResult, resultErr error) {
	apiKey, err := c.currentAPIKey(ctx)
	if err != nil {
		return nil, err
	}
	// Provider error details can echo credentials. Never pass the key into
	// application error responses, logs or persisted workflow failures.
	defer func() {
		if failure, ok := errors.AsType[*ErrDistanceCalculationFailed](resultErr); ok {
			failure.Reason = strings.ReplaceAll(failure.Reason, apiKey, "[redacted]")
		}
		if failure, ok := errors.AsType[*googleHTTPError](resultErr); ok {
			failure.Body = strings.ReplaceAll(failure.Body, apiKey, "[redacted]")
		}
	}()

	body, err := json.Marshal(googleMatrixRequest{
		Origins:           makeGoogleOrigins(origins),
		Destinations:      makeGoogleDestinations(destinations),
		TravelMode:        "DRIVE",
		RoutingPreference: "TRAFFIC_UNAWARE",
	})
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Goog-Api-Key", apiKey)
	req.Header.Set("X-Goog-FieldMask", googleRouteMatrixFieldMask)

	log.Printf("[GOOGLE] Route matrix request: origins=%d destinations=%d elements=%d", len(origins), len(destinations), len(origins)*len(destinations))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &googleTransportError{Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, providerErrorBodyLimit))
		return nil, &googleHTTPError{
			StatusCode: resp.StatusCode,
			RetryAfter: parseGoogleRetryAfter(resp.Header.Get("Retry-After")),
			Body:       strings.TrimSpace(string(responseBody)),
		}
	}

	responseBody := &googleResponseReader{Reader: resp.Body}
	elements, err := parseGoogleMatrixElements(responseBody)
	if responseBody.Err != nil {
		return nil, &googleTransportError{Cause: responseBody.Err}
	}
	if err != nil {
		return nil, err
	}

	results := make(map[matrixIndex]DistanceResult, len(elements))
	for _, element := range elements {
		if element.OriginIndex < 0 || element.OriginIndex >= len(origins) || element.DestinationIndex < 0 || element.DestinationIndex >= len(destinations) {
			return nil, &ErrDistanceCalculationFailed{Reason: "Google route matrix response contained out-of-range index"}
		}
		if element.Status.Code != 0 {
			return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate this route. Try another selection.", Cause: fmt.Errorf("google matrix status %d: %s", element.Status.Code, element.Status.Message)}
		}
		if element.Condition != "" && element.Condition != "ROUTE_EXISTS" {
			return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate this route. Try another selection."}
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

type googleTransportError struct {
	Cause error
}

type googleResponseReader struct {
	io.Reader
	Err error
}

func (r *googleResponseReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.Err = err
	}
	return n, err
}

func (e *googleTransportError) Error() string {
	return "Could not reach the route service. Please try again."
}

func (e *googleTransportError) Unwrap() error {
	return e.Cause
}

type googleHTTPError struct {
	StatusCode int
	RetryAfter time.Duration
	Body       string
}

func (e *googleHTTPError) Error() string {
	return fmt.Sprintf("Google route matrix HTTP %d", e.StatusCode)
}

type googleMatrixError struct {
	*ErrDistanceCalculationFailed
	Cause error
}

func (e *googleMatrixError) Unwrap() []error {
	errs := []error{e.ErrDistanceCalculationFailed}
	if e.Cause != nil {
		errs = append(errs, e.Cause)
	}
	return errs
}

type googleRetryError struct {
	*googleMatrixError
}

func isGoogleRetryableFailure(err error) bool {
	_, ok := errors.AsType[*googleRetryError](err)
	return ok
}

func googleRetryDelay(ctx context.Context, err error, attempt int) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}

	if _, ok := errors.AsType[*googleTransportError](err); ok {
		return jitteredGoogleBackoff(attempt), true
	}

	var httpErr *googleHTTPError
	if !errors.As(err, &httpErr) || !isGoogleRetryableStatus(httpErr.StatusCode) {
		return 0, false
	}
	delay := max(jitteredGoogleBackoff(attempt), httpErr.RetryAfter)
	return delay, true
}

func isGoogleRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func jitteredGoogleBackoff(attempt int) time.Duration {
	base := googleRetryBaseDelay << attempt
	//nolint:gosec // G404: retry jitter does not need cryptographic randomness.
	return base + time.Duration(rand.Int64N(int64(base/2)+1))
}

func waitForGoogleRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseGoogleRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 0 {
			if seconds > int64(maxGoogleRetryAfter/time.Second) {
				return maxGoogleRetryAfter
			}
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

func googleMatrixPublicError(err error) *googleMatrixError {
	if distanceErr, ok := errors.AsType[*ErrDistanceCalculationFailed](err); ok {
		return &googleMatrixError{ErrDistanceCalculationFailed: distanceErr}
	}
	return &googleMatrixError{
		ErrDistanceCalculationFailed: &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err},
		Cause:                        err,
	}
}

func (c *googleCalculator) currentAPIKey(ctx context.Context) (string, error) {
	if c.apiKey == nil {
		return "", fmt.Errorf("%w: Google Maps API key is missing", ErrProviderNotConfigured)
	}
	apiKey, err := c.apiKey(ctx)
	if err != nil {
		return "", err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("%w: Google Maps API key is missing; ask an administrator to configure it in Settings", ErrProviderNotConfigured)
	}
	return apiKey, nil
}

func matrixNeedsProvider(points []models.Coordinates) bool {
	for i, origin := range points {
		for j, dest := range points {
			if i != j && !SamePoint(origin, dest) {
				return true
			}
		}
	}
	return false
}

func destinationsNeedProvider(origin models.Coordinates, destinations []models.Coordinates) bool {
	for _, dest := range destinations {
		if !SamePoint(origin, dest) {
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
		return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
	}

	decoder := json.NewDecoder(reader)
	if first == '[' {
		var elements []googleMatrixElement
		if err := decoder.Decode(&elements); err != nil {
			return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
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
			return nil, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
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
		return 0, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again."}
	}
	seconds, err := time.ParseDuration(value)
	if err != nil {
		return 0, &ErrDistanceCalculationFailed{Reason: "Could not calculate routes. Please try again.", Cause: err}
	}
	return math.Round(seconds.Seconds()*1000) / 1000, nil
}

// IsTemporary identifies failures that may recover without changing a selection.
func IsTemporary(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if httpErr, ok := errors.AsType[*googleHTTPError](err); ok {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 || httpErr.StatusCode == http.StatusRequestTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return isGoogleRetryableFailure(err)
}
