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

// NewOSRMCalculator creates a new OSRM distance calculator with caching
func NewOSRMCalculator(cache database.DistanceCacheRepository) DistanceCalculator {
	return &osrmCalculator{
		baseURL: "https://router.project-osrm.org",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		cache: cache,
	}
}

func (c *osrmCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*DistanceResult, error) {
	// Quick check: same point to same point = 0 (with rounding tolerance)
	// Round to 5 decimal places (~1m precision) to match cache key rounding
	roundLat := func(v float64) float64 { return float64(int(v*100000+0.5)) / 100000 }
	roundLng := func(v float64) float64 { return float64(int(v*100000+0.5)) / 100000 }

	if roundLat(origin.Lat) == roundLat(dest.Lat) && roundLng(origin.Lng) == roundLng(dest.Lng) {
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

// maxOSRMCoordinates is the maximum number of coordinates OSRM public API accepts
const maxOSRMCoordinates = 80

func (c *osrmCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error) {
	n := len(points)
	if n == 0 {
		return [][]DistanceResult{}, nil
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
	}

	// First, check cache for all pairs
	var missingPairs []struct {
		i, j int
	}

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
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
			} else {
				missingPairs = append(missingPairs, struct{ i, j int }{i, j})
			}
		}
	}

	if len(missingPairs) == 0 {
		log.Printf("[OSRM] Distance matrix all cached: points=%d", n)
		return matrix, nil
	}

	log.Printf("[OSRM] Distance matrix request: points=%d cached=%d missing=%d", n, n*n-len(missingPairs), len(missingPairs))

	// If points fit in one request, do single request
	if n <= maxOSRMCoordinates {
		return c.fetchDistanceMatrixSingle(ctx, points, matrix)
	}

	// Otherwise, batch requests
	log.Printf("[OSRM] Using batched requests: points=%d batches=%d", n, (n+maxOSRMCoordinates-1)/maxOSRMCoordinates)
	return c.fetchDistanceMatrixBatched(ctx, points, matrix)
}

// fetchDistanceMatrixSingle fetches distance matrix in a single OSRM request
func (c *osrmCalculator) fetchDistanceMatrixSingle(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult) ([][]DistanceResult, error) {
	n := len(points)
	coords := make([]string, n)
	for i, p := range points {
		coords[i] = fmt.Sprintf("%.6f,%.6f", p.Lng, p.Lat)
	}

	coordsStr := strings.Join(coords, ";")
	queryURL := fmt.Sprintf("%s/table/v1/driving/%s?annotations=distance,duration", c.baseURL, coordsStr)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Failed to create OSRM request: points=%d err=%v", n, err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] OSRM API request failed: points=%d err=%v", n, err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] OSRM API error: points=%d status=%d body=%s", n, resp.StatusCode, string(body))
		return nil, &ErrDistanceCalculationFailed{
			Reason: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var osrmResp osrmTableResponse
	if err := json.NewDecoder(resp.Body).Decode(&osrmResp); err != nil {
		log.Printf("[ERROR] Failed to decode OSRM response: points=%d err=%v", n, err)
		return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
	}

	if osrmResp.Code != "Ok" {
		log.Printf("[ERROR] OSRM returned error code: points=%d code=%s", n, osrmResp.Code)
		return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("OSRM error: %s", osrmResp.Code)}
	}

	log.Printf("[OSRM] Distance matrix response: points=%d code=%s", n, osrmResp.Code)

	var cacheEntries []models.DistanceCacheEntry
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && osrmResp.Distances[i][j] > 0 {
				matrix[i][j] = DistanceResult{
					DistanceMeters: osrmResp.Distances[i][j],
					DurationSecs:   osrmResp.Durations[i][j],
				}

				cacheEntries = append(cacheEntries, models.DistanceCacheEntry{
					Origin:         points[i],
					Destination:    points[j],
					DistanceMeters: osrmResp.Distances[i][j],
					DurationSecs:   osrmResp.Durations[i][j],
				})
			}
		}
	}

	if len(cacheEntries) > 0 {
		if err := c.cache.SetBatch(ctx, cacheEntries); err != nil {
			return nil, err
		}
	}

	return matrix, nil
}

// fetchDistanceMatrixBatched fetches distance matrix using multiple batched OSRM requests
func (c *osrmCalculator) fetchDistanceMatrixBatched(ctx context.Context, points []models.Coordinates, matrix [][]DistanceResult) ([][]DistanceResult, error) {
	n := len(points)

	// Create batches of point indices
	var batches [][]int
	for i := 0; i < n; i += maxOSRMCoordinates {
		end := i + maxOSRMCoordinates
		if end > n {
			end = n
		}
		batch := make([]int, end-i)
		for j := i; j < end; j++ {
			batch[j-i] = j
		}
		batches = append(batches, batch)
	}

	log.Printf("[OSRM] Created %d batches for %d points", len(batches), n)

	// For each pair of batches (including same batch), fetch distances
	var allCacheEntries []models.DistanceCacheEntry
	requestCount := 0

	for bi, batchI := range batches {
		for bj, batchJ := range batches {
			// Collect unique points for this batch pair
			pointSet := make(map[int]bool)
			for _, idx := range batchI {
				pointSet[idx] = true
			}
			for _, idx := range batchJ {
				pointSet[idx] = true
			}

			// Convert to slice and create coordinate mapping
			var batchPoints []models.Coordinates
			globalToLocal := make(map[int]int)
			localIdx := 0
			for idx := range pointSet {
				globalToLocal[idx] = localIdx
				batchPoints = append(batchPoints, points[idx])
				localIdx++
			}

			if len(batchPoints) == 0 {
				continue
			}

			// Build OSRM request
			coords := make([]string, len(batchPoints))
			for i, p := range batchPoints {
				coords[i] = fmt.Sprintf("%.6f,%.6f", p.Lng, p.Lat)
			}

			// Build sources and destinations indices
			var sources, destinations []string
			for _, idx := range batchI {
				sources = append(sources, fmt.Sprintf("%d", globalToLocal[idx]))
			}
			for _, idx := range batchJ {
				destinations = append(destinations, fmt.Sprintf("%d", globalToLocal[idx]))
			}

			coordsStr := strings.Join(coords, ";")
			queryURL := fmt.Sprintf("%s/table/v1/driving/%s?annotations=distance,duration&sources=%s&destinations=%s",
				c.baseURL, coordsStr, strings.Join(sources, ";"), strings.Join(destinations, ";"))

			req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
			if err != nil {
				return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
			}

			resp, err := c.httpClient.Do(req)
			if err != nil {
				return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
			}

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return nil, &ErrDistanceCalculationFailed{
					Reason: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
				}
			}

			var osrmResp osrmTableResponse
			if err := json.NewDecoder(resp.Body).Decode(&osrmResp); err != nil {
				resp.Body.Close()
				return nil, &ErrDistanceCalculationFailed{Reason: err.Error()}
			}
			resp.Body.Close()

			if osrmResp.Code != "Ok" {
				return nil, &ErrDistanceCalculationFailed{Reason: fmt.Sprintf("OSRM error: %s", osrmResp.Code)}
			}

			requestCount++

			// Fill in matrix values
			for si, srcIdx := range batchI {
				for di, dstIdx := range batchJ {
					if srcIdx == dstIdx {
						continue
					}
					dist := osrmResp.Distances[si][di]
					dur := osrmResp.Durations[si][di]
					if dist > 0 {
						matrix[srcIdx][dstIdx] = DistanceResult{
							DistanceMeters: dist,
							DurationSecs:   dur,
						}
						allCacheEntries = append(allCacheEntries, models.DistanceCacheEntry{
							Origin:         points[srcIdx],
							Destination:    points[dstIdx],
							DistanceMeters: dist,
							DurationSecs:   dur,
						})
					}
				}
			}

			// Rate limit between batch requests
			if bi < len(batches)-1 || bj < len(batches)-1 {
				time.Sleep(100 * time.Millisecond)
			}
		}
	}

	log.Printf("[OSRM] Batched requests complete: requests=%d entries=%d", requestCount, len(allCacheEntries))

	if len(allCacheEntries) > 0 {
		if err := c.cache.SetBatch(ctx, allCacheEntries); err != nil {
			return nil, err
		}
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
