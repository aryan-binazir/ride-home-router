package geocoding

import (
	"context"
	"log"
	"time"
)

type fallbackGeocoder struct {
	primary  Geocoder
	fallback Geocoder
}

const censusSearchFallbackTimeout = 4 * time.Second

func (g *fallbackGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	result, err := g.primary.Geocode(ctx, address)
	if err == nil {
		return result, nil
	}
	if !isNoResultsError(err) || !looksLikeUSAddressQuery(address) {
		return nil, err
	}

	log.Printf("[GEOCODING] Falling back to Census geocoder")
	fallbackResult, fallbackErr := g.fallback.Geocode(ctx, address)
	if fallbackErr != nil {
		log.Printf("[GEOCODING] Census geocode fallback failed")
		return nil, err
	}

	return fallbackResult, nil
}

func (g *fallbackGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	return geocodeWithRetry(ctx, address, maxRetries, g.Geocode)
}

func (g *fallbackGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	results, err := g.primary.Search(ctx, query, limit)
	if err != nil || len(results) > 0 || !isSpecificUSAddressQuery(query) {
		return results, err
	}

	log.Printf("[GEOCODING] Falling back to Census search")
	fallbackCtx, cancel := context.WithTimeout(ctx, censusSearchFallbackTimeout)
	defer cancel()

	fallbackResults, fallbackErr := g.fallback.Search(fallbackCtx, query, limit)
	if fallbackErr != nil {
		log.Printf("[GEOCODING] Census search fallback failed")
		return results, nil
	}

	return fallbackResults, nil
}
