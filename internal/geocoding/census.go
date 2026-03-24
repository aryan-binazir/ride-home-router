package geocoding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"ride-home-router/internal/models"
)

type censusGeocoder struct {
	baseURL    string
	httpClient *http.Client
}

type censusResponse struct {
	Result struct {
		AddressMatches []censusAddressMatch `json:"addressMatches"`
	} `json:"result"`
}

type censusAddressMatch struct {
	Coordinates struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	} `json:"coordinates"`
	MatchedAddress    string                  `json:"matchedAddress"`
	AddressComponents censusAddressComponents `json:"addressComponents"`
}

type censusAddressComponents struct {
	FromAddress     string `json:"fromAddress"`
	PreDirection    string `json:"preDirection"`
	PreType         string `json:"preType"`
	StreetName      string `json:"streetName"`
	SuffixType      string `json:"suffixType"`
	SuffixDirection string `json:"suffixDirection"`
	SuffixQualifier string `json:"suffixQualifier"`
	City            string `json:"city"`
	State           string `json:"state"`
	ZIP             string `json:"zip"`
}

func newCensusGeocoder(baseURL string, httpClient *http.Client) *censusGeocoder {
	return &censusGeocoder{
		baseURL:    baseURL,
		httpClient: httpClient,
	}
}

func (g *censusGeocoder) Geocode(ctx context.Context, address string) (*GeocodingResult, error) {
	results, err := g.Search(ctx, address, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, &ErrGeocodingFailed{Address: address, Reason: "no results found", Cause: ErrNoGeocodingResults}
	}

	result := results[0]
	return &result, nil
}

func (g *censusGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error) {
	return geocodeWithRetry(ctx, address, maxRetries, g.Geocode)
}

func (g *censusGeocoder) Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error) {
	queryURL := fmt.Sprintf("%s/geocoder/locations/onelineaddress?address=%s&benchmark=Public_AR_Current&format=json",
		strings.TrimRight(g.baseURL, "/"),
		url.QueryEscape(query),
	)
	log.Printf("[GEOCODING] Census request: query=%s url=%s", query, queryURL)

	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		log.Printf("[ERROR] Failed to create Census geocoding request: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}

	req.Header.Set("User-Agent", "RideHomeRouter/1.0")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		log.Printf("[ERROR] Census geocoding request failed: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[ERROR] Census geocoding API error: query=%s status=%d body=%s", query, resp.StatusCode, string(body))
		return nil, &ErrGeocodingFailed{
			Address: query,
			Reason:  fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
		}
	}

	var results censusResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[ERROR] Failed to decode Census geocoding response: query=%s err=%v", query, err)
		return nil, &ErrGeocodingFailed{Address: query, Reason: err.Error()}
	}

	if limit <= 0 || limit > len(results.Result.AddressMatches) {
		limit = len(results.Result.AddressMatches)
	}

	geocodingResults := make([]GeocodingResult, 0, limit)
	for _, match := range results.Result.AddressMatches[:limit] {
		geocodingResults = append(geocodingResults, GeocodingResult{
			Coords: models.Coordinates{
				Lat: match.Coordinates.Y,
				Lng: match.Coordinates.X,
			},
			DisplayName:      match.MatchedAddress,
			FormattedAddress: formatCensusAddressLabel(match),
		})
	}

	log.Printf("[GEOCODING] Census response: query=%s results_count=%d", query, len(geocodingResults))
	return geocodingResults, nil
}

func formatCensusAddressLabel(match censusAddressMatch) string {
	primary := joinNonEmpty(" ",
		match.AddressComponents.FromAddress,
		titleCaseWords(joinNonEmpty(" ",
			match.AddressComponents.PreDirection,
			match.AddressComponents.PreType,
			match.AddressComponents.StreetName,
			match.AddressComponents.SuffixType,
			match.AddressComponents.SuffixDirection,
			match.AddressComponents.SuffixQualifier,
		)),
	)

	locality := titleCaseWords(match.AddressComponents.City)
	regionAndPostcode := joinNonEmpty(" ", strings.ToUpper(strings.TrimSpace(match.AddressComponents.State)), strings.TrimSpace(match.AddressComponents.ZIP))
	parts := uniqueNonEmpty(primary, locality, regionAndPostcode)
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}

	parts = strings.Split(match.MatchedAddress, ",")
	if len(parts) == 0 {
		return strings.TrimSpace(match.MatchedAddress)
	}

	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if i <= 1 {
			parts[i] = titleCaseWords(parts[i])
		} else {
			parts[i] = strings.ToUpper(parts[i])
		}
	}

	return strings.Join(uniqueNonEmpty(parts...), ", ")
}

func titleCaseWords(value string) string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	for i, word := range words {
		segments := strings.Split(word, "-")
		for j, segment := range segments {
			segments[j] = titleCaseToken(segment)
		}
		words[i] = strings.Join(segments, "-")
	}

	return strings.Join(words, " ")
}

func titleCaseToken(value string) string {
	if value == "" {
		return ""
	}

	parts := strings.Split(value, "'")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}

	return strings.Join(parts, "'")
}
