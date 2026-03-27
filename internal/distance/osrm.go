package distance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

// DistanceResult contains the result of a distance calculation
type DistanceResult struct {
	DistanceMeters float64
	DurationSecs   float64
}

// DistanceCalculator provides distance calculations between coordinates
type DistanceCalculator interface {
	GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error)
	GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error)
	GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error)
	PrewarmCache(ctx context.Context, points []models.Coordinates) error
}

// ErrDistanceCalculationFailed is returned when OSRM API fails
type ErrDistanceCalculationFailed struct {
	Origin models.Coordinates
	Dest   models.Coordinates
	Reason string
}

func (e *ErrDistanceCalculationFailed) Error() string {
	return fmt.Sprintf("distance calculation failed: %s", e.Reason)
}

type osrmCalculator struct {
	baseURL    string
	httpClient *http.Client
	cache      database.DistanceCacheRepository
}

type osrmTableResponse struct {
	Code      string      `json:"code"`
	Distances [][]float64 `json:"distances"`
	Durations [][]float64 `json:"durations"`
}

type matrixPair struct {
	source      int
	destination int
}

type missingMatrixPlan struct {
	pairs        map[matrixPair]struct{}
	sources      []int
	destinations []int
	count        int
}

// NewOSRMCalculator creates a new OSRM distance calculator with caching
func NewOSRMCalculator(cache database.DistanceCacheRepository) DistanceCalculator {
	return &osrmCalculator{
		baseURL: "https://router.project-osrm.org",
		httpClient: &http.Client{
			Timeout: osrmClientTimeout,
		},
		cache: cache,
	}
}

func (c *osrmCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error) {
	// Quick check: same point to same point = 0 (with rounding tolerance)
	// Round to 5 decimal places (~1m precision) to match cache key rounding
	if models.RoundCoordinate(origin.Lat) == models.RoundCoordinate(dest.Lat) &&
		models.RoundCoordinate(origin.Lng) == models.RoundCoordinate(dest.Lng) {
		return &DistanceResult{DistanceMeters: 0, DurationSecs: 0}, nil
	}

	cached, err := c.cache.Get(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	if cached != nil {
		// Don't log every cache hit - too noisy
		return &DistanceResult{
			DistanceMeters: cached.DistanceMeters,
			DurationSecs:   cached.DurationSecs,
		}, nil
	}

	log.Printf("[OSRM] Cache miss: origin=(%.6f,%.6f) dest=(%.6f,%.6f)", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
	results, err := c.GetDistancesFromPoint(ctx, origin, []models.Coordinates{dest})
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		log.Printf("[ERROR] Distance calculation returned no results: origin=(%.6f,%.6f) dest=(%.6f,%.6f)", origin.Lat, origin.Lng, dest.Lat, dest.Lng)
		return nil, &ErrDistanceCalculationFailed{
			Origin: origin,
			Dest:   dest,
			Reason: "no results returned",
		}
	}

	log.Printf("[OSRM] Distance calculated: origin=(%.6f,%.6f) dest=(%.6f,%.6f) distance=%.0f", origin.Lat, origin.Lng, dest.Lat, dest.Lng, results[0].DistanceMeters)
	return &results[0], nil
}

const (
	// maxOSRMCoordinates is the maximum number of coordinates OSRM public API accepts
	maxOSRMCoordinates   = 80
	osrmClientTimeout    = 30 * time.Second
	osrmBatchRateDelay   = 100 * time.Millisecond
)

func (c *osrmCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error) {
	n := len(points)
	if n == 0 {
		return [][]DistanceResult{}, nil
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
	}

	missingPlan, err := c.hydrateMatrixFromCache(ctx, points, matrix)
	if err != nil {
		return nil, err
	}

	if missingPlan.count == 0 {
		log.Printf("[OSRM] Distance matrix all cached: points=%d", n)
		return matrix, nil
	}

	log.Printf("[OSRM] Distance matrix request: points=%d cached=%d missing=%d", n, n*n-missingPlan.count, missingPlan.count)

	// If points fit in one request, do single request
	if n <= maxOSRMCoordinates {
		return c.fetchDistanceMatrixSingle(ctx, points, matrix, missingPlan)
	}

	// Otherwise, batch requests
	log.Printf("[OSRM] Using batched requests: points=%d batches=%d", n, (n+maxOSRMCoordinates-1)/maxOSRMCoordinates)
	return c.fetchDistanceMatrixBatched(ctx, points, matrix, missingPlan)
}

// fetchDistanceMatrixSingle fetches distance matrix in a single OSRM request
func (c *osrmCalculator) fetchDistanceMatrixSingle(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult, missingPlan *missingMatrixPlan) ([][]DistanceResult, error) {
	n := len(points)
	fullCells := n * (n - 1)
	partialCells := len(missingPlan.sources) * len(missingPlan.destinations)

	if partialCells < fullCells {
		log.Printf("[OSRM] Distance matrix partial request: points=%d missing=%d sources=%d destinations=%d",
			n, missingPlan.count, len(missingPlan.sources), len(missingPlan.destinations))
		cacheEntries, err := c.fetchTableIntoMatrix(ctx, points, missingPlan.sources, missingPlan.destinations, missingPlan.sources, missingPlan.destinations, points, matrix, missingPlan.pairs)
		if err != nil {
			return nil, err
		}
		if err := c.persistCacheEntries(ctx, cacheEntries); err != nil {
			return nil, err
		}
		return matrix, nil
	}

	log.Printf("[OSRM] Distance matrix full request: points=%d missing=%d", n, missingPlan.count)
	allIndices := makeRangeIndices(n)
	cacheEntries, err := c.fetchTableIntoMatrix(ctx, points, nil, nil, allIndices, allIndices, points, matrix, nil)
	if err != nil {
		return nil, err
	}
	if err := c.persistCacheEntries(ctx, cacheEntries); err != nil {
		return nil, err
	}

	return matrix, nil
}

// fetchDistanceMatrixBatched fetches distance matrix using multiple batched OSRM requests
func (c *osrmCalculator) fetchDistanceMatrixBatched(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult, missingPlan *missingMatrixPlan) ([][]DistanceResult, error) {
	n := len(points)

	// Create batches of point indices
	var batches [][]int
	for i := 0; i < n; i += maxOSRMCoordinates {
		end := min(i+maxOSRMCoordinates, n)
		batch := make([]int, end-i)
		for j := range batch {
			batch[j] = i + j
		}
		batches = append(batches, batch)
	}

	log.Printf("[OSRM] Created %d batches for %d points", len(batches), n)

	// For each pair of batches (including same batch), fetch distances
	var allCacheEntries []models.DistanceCacheEntry
	requestCount := 0

	for bi, batchI := range batches {
		for bj, batchJ := range batches {
			blockMissingPlan := collectMissingBlockPlan(batchI, batchJ, missingPlan.pairs)
			if blockMissingPlan.count == 0 {
				continue
			}

			batchPoints, globalToLocal := buildBatchPoints(points, batchI, batchJ)
			fullCells := len(batchI) * len(batchJ)
			if bi == bj {
				fullCells -= len(batchI)
			}
			partialCells := len(blockMissingPlan.sources) * len(blockMissingPlan.destinations)

			querySourcesGlobal := batchI
			queryDestinationsGlobal := batchJ
			blockMissingPairs := map[matrixPair]struct{}(nil)
			if partialCells < fullCells {
				log.Printf("[OSRM] Batched partial request: batch=(%d,%d) missing=%d sources=%d destinations=%d",
					bi, bj, blockMissingPlan.count, len(blockMissingPlan.sources), len(blockMissingPlan.destinations))
				querySourcesGlobal = blockMissingPlan.sources
				queryDestinationsGlobal = blockMissingPlan.destinations
				blockMissingPairs = blockMissingPlan.pairs
			}

			querySourcesLocal := mapIndicesToLocal(querySourcesGlobal, globalToLocal)
			queryDestinationsLocal := mapIndicesToLocal(queryDestinationsGlobal, globalToLocal)
			cacheEntries, err := c.fetchTableIntoMatrix(ctx, batchPoints, querySourcesLocal, queryDestinationsLocal, querySourcesGlobal, queryDestinationsGlobal, points, matrix, blockMissingPairs)
			if err != nil {
				return nil, err
			}

			requestCount++
			allCacheEntries = append(allCacheEntries, cacheEntries...)

			// Rate limit between batch requests
			if bi < len(batches)-1 || bj < len(batches)-1 {
				time.Sleep(osrmBatchRateDelay)
			}
		}
	}

	log.Printf("[OSRM] Batched requests complete: requests=%d entries=%d", requestCount, len(allCacheEntries))

	if err := c.persistCacheEntries(ctx, allCacheEntries); err != nil {
		return nil, err
	}

	return matrix, nil
}

func (c *osrmCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]DistanceResult, error) {
	if len(destinations) == 0 {
		return []DistanceResult{}, nil
	}

	allPoints := append([]models.Coordinates{origin}, destinations...)
	matrix, err := c.GetDistanceMatrix(ctx, allPoints)
	if err != nil {
		return nil, err
	}

	results := make([]DistanceResult, len(destinations))
	for i := range destinations {
		results[i] = matrix[0][i+1]
	}

	return results, nil
}

func (c *osrmCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	_, err := c.GetDistanceMatrix(ctx, points)
	return err
}

func (c *osrmCalculator) hydrateMatrixFromCache(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult) (*missingMatrixPlan, error) {
	n := len(points)
	plan := &missingMatrixPlan{
		pairs: make(map[matrixPair]struct{}),
	}
	sourceSeen := make([]bool, n)
	destinationSeen := make([]bool, n)

	for i := range n {
		for j := range n {
			if i == j {
				matrix[i][j] = DistanceResult{DistanceMeters: 0, DurationSecs: 0}
				continue
			}

			cached, err := c.cache.Get(ctx, points[i], points[j])
			if err != nil {
				return nil, err
			}
			if cached != nil {
				matrix[i][j] = DistanceResult{
					DistanceMeters: cached.DistanceMeters,
					DurationSecs:   cached.DurationSecs,
				}
				continue
			}

			pair := matrixPair{source: i, destination: j}
			plan.pairs[pair] = struct{}{}
			plan.count++
			if !sourceSeen[i] {
				sourceSeen[i] = true
				plan.sources = append(plan.sources, i)
			}
			if !destinationSeen[j] {
				destinationSeen[j] = true
				plan.destinations = append(plan.destinations, j)
			}
		}
	}

	return plan, nil
}

func (c *osrmCalculator) fetchTableIntoMatrix(
	ctx context.Context,
	queryPoints []models.Coordinates,
	querySources []int,
	queryDestinations []int,
	targetSources []int,
	targetDestinations []int,
	matrixPoints []models.Coordinates,
	matrix [][]DistanceResult,
	missingPairs map[matrixPair]struct{},
) ([]models.DistanceCacheEntry, error) {
	response, err := c.requestTable(ctx, queryPoints, querySources, queryDestinations)
	if err != nil {
		return nil, err
	}

	cacheEntries := make([]models.DistanceCacheEntry, 0, len(targetSources)*len(targetDestinations))
	for si, srcIdx := range targetSources {
		for di, dstIdx := range targetDestinations {
			if srcIdx == dstIdx {
				continue
			}
			if missingPairs != nil {
				if _, ok := missingPairs[matrixPair{source: srcIdx, destination: dstIdx}]; !ok {
					continue
				}
			}

			dist := response.Distances[si][di]
			dur := response.Durations[si][di]
			if dist <= 0 {
				continue
			}

			matrix[srcIdx][dstIdx] = DistanceResult{
				DistanceMeters: dist,
				DurationSecs:   dur,
			}
			cacheEntries = append(cacheEntries, models.DistanceCacheEntry{
				Origin:         matrixPoints[srcIdx],
				Destination:    matrixPoints[dstIdx],
				DistanceMeters: dist,
				DurationSecs:   dur,
			})
		}
	}

	return cacheEntries, nil
}

func (c *osrmCalculator) requestTable(ctx context.Context, points []models.Coordinates, sources []int, destinations []int) (*osrmTableResponse, error) {
	coords := make([]string, len(points))
	for i, p := range points {
		coords[i] = fmt.Sprintf("%.6f,%.6f", p.Lng, p.Lat)
	}

	queryURL := fmt.Sprintf("%s/table/v1/driving/%s?annotations=distance,duration", c.baseURL, strings.Join(coords, ";"))
	if len(sources) > 0 {
		queryURL += "&sources=" + joinIndices(sources)
	}
	if len(destinations) > 0 {
		queryURL += "&destinations=" + joinIndices(destinations)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Failed to create OSRM request: points=%d err=%v", len(points), err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] OSRM API request failed: points=%d err=%v", len(points), err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] OSRM API error: points=%d status=%d body=%s", len(points), resp.StatusCode, string(body))
		return nil, &ErrDistanceCalculationFailed{
			Reason: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var osrmResp osrmTableResponse
	if err := json.NewDecoder(resp.Body).Decode(&osrmResp); err != nil {
		log.Printf("[ERROR] Failed to decode OSRM response: points=%d err=%v", len(points), err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	if osrmResp.Code != "Ok" {
		log.Printf("[ERROR] OSRM returned error code: points=%d code=%s", len(points), osrmResp.Code)
		return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("OSRM error: %s", osrmResp.Code)}
	}

	log.Printf("[OSRM] Distance matrix response: points=%d code=%s", len(points), osrmResp.Code)
	return &osrmResp, nil
}

func (c *osrmCalculator) persistCacheEntries(ctx context.Context, cacheEntries []models.DistanceCacheEntry) error {
	if len(cacheEntries) == 0 {
		return nil
	}
	return c.cache.SetBatch(ctx, cacheEntries)
}

func collectMissingBlockPlan(batchI, batchJ []int, missingPairs map[matrixPair]struct{}) *missingMatrixPlan {
	plan := &missingMatrixPlan{
		pairs: make(map[matrixPair]struct{}),
	}
	sourceSeen := make(map[int]struct{}, len(batchI))
	destinationSeen := make(map[int]struct{}, len(batchJ))

	for _, srcIdx := range batchI {
		for _, dstIdx := range batchJ {
			if srcIdx == dstIdx {
				continue
			}

			pair := matrixPair{source: srcIdx, destination: dstIdx}
			if _, ok := missingPairs[pair]; !ok {
				continue
			}

			plan.pairs[pair] = struct{}{}
			plan.count++
			if _, ok := sourceSeen[srcIdx]; !ok {
				sourceSeen[srcIdx] = struct{}{}
				plan.sources = append(plan.sources, srcIdx)
			}
			if _, ok := destinationSeen[dstIdx]; !ok {
				destinationSeen[dstIdx] = struct{}{}
				plan.destinations = append(plan.destinations, dstIdx)
			}
		}
	}

	return plan
}

func buildBatchPoints(points []models.Coordinates, batchI, batchJ []int) ([]models.Coordinates, map[int]int) {
	batchPoints := make([]models.Coordinates, 0, len(batchI)+len(batchJ))
	globalToLocal := make(map[int]int, len(batchI)+len(batchJ))
	addPoint := func(idx int) {
		if _, ok := globalToLocal[idx]; ok {
			return
		}
		globalToLocal[idx] = len(batchPoints)
		batchPoints = append(batchPoints, points[idx])
	}

	for _, idx := range batchI {
		addPoint(idx)
	}
	for _, idx := range batchJ {
		addPoint(idx)
	}

	return batchPoints, globalToLocal
}

func mapIndicesToLocal(indices []int, globalToLocal map[int]int) []int {
	local := make([]int, len(indices))
	for i, idx := range indices {
		local[i] = globalToLocal[idx]
	}
	return local
}

func makeRangeIndices(n int) []int {
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

func joinIndices(indices []int) string {
	parts := make([]string, len(indices))
	for i, idx := range indices {
		parts[i] = fmt.Sprintf("%d", idx)
	}
	return strings.Join(parts, ";")
}
