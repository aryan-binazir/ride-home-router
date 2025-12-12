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

func (c *osrmCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]DistanceResult, error) {
	n := len(points)
	if n == 0 {
		return [][]DistanceResult{}, nil
	}

	matrix := make([][]DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]DistanceResult, n)
	}

	var missingPairs []struct {
		i, j int
		models.Coordinates
		dest models.Coordinates
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
				missingPairs = append(missingPairs, struct {
					i, j int
					models.Coordinates
					dest models.Coordinates
				}{i, j, points[i], points[j]})
			}
		}
	}

	if len(missingPairs) == 0 {
		log.Printf("[OSRM] Distance matrix all cached: points=%d", n)
		return matrix, nil
	}

	log.Printf("[OSRM] Distance matrix request: points=%d cached=%d missing=%d", n, n*n-len(missingPairs), len(missingPairs))
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
