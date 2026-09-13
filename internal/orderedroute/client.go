// Package orderedroute measures a finished, ordered car route with the Google
// Routes API. Each request is billed per call, so the app can afford to
// measure every car of every plan while staying inside the free tier.
package orderedroute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultEndpoint = "https://routes.googleapis.com/directions/v2:computeRoutes"
	// Essentials pricing allows at most 10 intermediate waypoints per request:
	// 12 points (origin, 10 stops, destination) and 11 legs.
	maxPointsPerRequest   = 12
	maxConcurrentRequests = 16
	perCallTimeout        = 5 * time.Second
	responseBodyLimit     = 1 << 20
	fieldMask             = "routes.legs.distanceMeters,routes.legs.duration"
)

// ErrNotConfigured means no Google Maps key is available.
var ErrNotConfigured = errors.New("orderedroute: Google Maps API key is not configured")

// Request is one ordered route: origin, stops in order, destination. Requests
// sharing a Group (for example a car's route and its baseline) are reserved
// together so a car is never half paid for.
type Request struct {
	ID     string
	Group  string
	Points []models.Coordinates
}

// Leg is the measured hop between two consecutive points.
type Leg struct {
	DistanceMeters float64
	DurationSecs   float64
}

// Result carries the legs for one request, in point order, or its error.
type Result struct {
	ID   string
	Legs []Leg
	Err  error
}

// Reserve records attempts with the usage ledger before any dispatch.
type Reserve func(ctx context.Context, attempts int) error

// Client talks to Compute Routes with Essentials-only options.
type Client struct {
	key        distance.APIKeyProvider
	reserve    Reserve
	httpClient *http.Client
	endpoint   string
	semaphore  chan struct{}
}

// NewClient builds the production client. reserve may not be nil.
func NewClient(key distance.APIKeyProvider, reserve Reserve) *Client {
	return newClient(key, reserve, &http.Client{Timeout: perCallTimeout}, defaultEndpoint)
}

func newClient(key distance.APIKeyProvider, reserve Reserve, httpClient *http.Client, endpoint string) *Client {
	if reserve == nil {
		panic("orderedroute: usage reservation is required")
	}
	return &Client{key: key, reserve: reserve, httpClient: httpClient, endpoint: endpoint, semaphore: make(chan struct{}, maxConcurrentRequests)}
}

// RequestsFor reports how many billable calls measuring these points takes.
func RequestsFor(points int) int {
	if points < 2 {
		return 0
	}
	legs := points - 1
	return (legs + maxPointsPerRequest - 2) / (maxPointsPerRequest - 1)
}

// Measure returns one result per request, in order. Every billable attempt for
// the whole batch is reserved first; if the ledger refuses, nothing is sent.
func (c *Client) Measure(ctx context.Context, requests []Request) []Result {
	results := make([]Result, len(requests))
	for i := range requests {
		results[i].ID = requests[i].ID
	}
	failAll := func(err error) []Result {
		for i := range results {
			results[i].Err = err
		}
		return results
	}
	if len(requests) == 0 {
		return results
	}
	key, err := c.currentKey(ctx)
	if err != nil {
		return failAll(err)
	}
	for _, request := range requests {
		if len(request.Points) < 2 {
			return failAll(errors.New("orderedroute: a route needs at least an origin and a destination"))
		}
	}

	// Each group (a car) reserves its attempts together, so a nearly exhausted
	// month still measures as many whole cars as the allowance permits.
	groups := make(map[string][]int)
	var order []string
	for i, request := range requests {
		group := request.Group
		if group == "" {
			group = request.ID
		}
		if _, seen := groups[group]; !seen {
			order = append(order, group)
		}
		groups[group] = append(groups[group], i)
	}
	var wg sync.WaitGroup
	for _, group := range order {
		indexes := groups[group]
		wg.Go(func() {
			attempts := 0
			for _, index := range indexes {
				attempts += RequestsFor(len(requests[index].Points))
			}
			if err := c.reserve(ctx, attempts); err != nil {
				for _, index := range indexes {
					results[index].Err = fmt.Errorf("orderedroute: %w", err)
				}
				return
			}
			var inner sync.WaitGroup
			for _, index := range indexes {
				inner.Go(func() {
					legs, err := c.measureRoute(ctx, key, requests[index].Points)
					results[index].Legs, results[index].Err = legs, err
				})
			}
			inner.Wait()
		})
	}
	wg.Wait()
	return results
}

func (c *Client) currentKey(ctx context.Context) (string, error) {
	if c.key == nil {
		return "", ErrNotConfigured
	}
	key, err := c.key(ctx)
	if err != nil {
		return "", fmt.Errorf("orderedroute: credentials unavailable: %w", err)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", ErrNotConfigured
	}
	return key, nil
}

// measureRoute splits the point sequence into chunks that share one endpoint,
// so the concatenated legs cover every hop exactly once.
func (c *Client) measureRoute(ctx context.Context, key string, points []models.Coordinates) ([]Leg, error) {
	legs := make([]Leg, 0, len(points)-1)
	for start := 0; start < len(points)-1; start += maxPointsPerRequest - 1 {
		end := min(start+maxPointsPerRequest, len(points))
		chunk, err := c.computeRoute(ctx, key, points[start:end])
		if err != nil {
			return nil, err
		}
		legs = append(legs, chunk...)
	}
	if len(legs) != len(points)-1 {
		return nil, errors.New("orderedroute: provider returned an unexpected number of legs")
	}
	return legs, nil
}

type latLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type waypoint struct {
	Location struct {
		LatLng latLng `json:"latLng"`
	} `json:"location"`
}

func newWaypoint(c models.Coordinates) waypoint {
	var w waypoint
	w.Location.LatLng = latLng{Latitude: c.Lat, Longitude: c.Lng}
	return w
}

type computeRoutesRequest struct {
	Origin            waypoint   `json:"origin"`
	Destination       waypoint   `json:"destination"`
	Intermediates     []waypoint `json:"intermediates,omitempty"`
	TravelMode        string     `json:"travelMode"`
	RoutingPreference string     `json:"routingPreference"`
	Units             string     `json:"units"`
}

type computeRoutesResponse struct {
	Routes []struct {
		Legs []struct {
			DistanceMeters float64 `json:"distanceMeters"`
			Duration       string  `json:"duration"`
		} `json:"legs"`
	} `json:"routes"`
}

func (c *Client) computeRoute(ctx context.Context, key string, points []models.Coordinates) ([]Leg, error) {
	select {
	case c.semaphore <- struct{}{}:
		defer func() { <-c.semaphore }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	callCtx, cancel := context.WithTimeout(ctx, perCallTimeout)
	defer cancel()

	payload := computeRoutesRequest{Origin: newWaypoint(points[0]), Destination: newWaypoint(points[len(points)-1]), TravelMode: "DRIVE", RoutingPreference: "TRAFFIC_UNAWARE", Units: "METRIC"}
	for _, point := range points[1 : len(points)-1] {
		payload.Intermediates = append(payload.Intermediates, newWaypoint(point))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("orderedroute: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("orderedroute: request creation failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", key)
	req.Header.Set("X-Goog-FieldMask", fieldMask)

	started := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[ROUTES] measure outcome=request_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, &transportError{cause: unwrapURLError(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ROUTES] measure outcome=http_error status=%d duration=%s", resp.StatusCode, time.Since(started).Round(time.Millisecond))
		return nil, &StatusError{Status: resp.StatusCode}
	}
	var decoded computeRoutesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&decoded); err != nil {
		log.Printf("[ROUTES] measure outcome=decode_failed duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, errors.New("orderedroute: malformed provider response")
	}
	if len(decoded.Routes) != 1 || len(decoded.Routes[0].Legs) != len(points)-1 {
		log.Printf("[ROUTES] measure outcome=unexpected_shape duration=%s", time.Since(started).Round(time.Millisecond))
		return nil, errors.New("orderedroute: provider returned an unexpected number of legs")
	}
	legs := make([]Leg, len(points)-1)
	for i, leg := range decoded.Routes[0].Legs {
		seconds, err := parseDuration(leg.Duration)
		if err != nil || leg.DistanceMeters < 0 {
			return nil, errors.New("orderedroute: malformed provider response")
		}
		legs[i] = Leg{DistanceMeters: leg.DistanceMeters, DurationSecs: seconds}
	}
	log.Printf("[ROUTES] measure outcome=success legs=%d duration=%s", len(legs), time.Since(started).Round(time.Millisecond))
	return legs, nil
}

// Routes API durations look like "1234s" or "12.5s"; proto3 JSON may omit a
// zero duration entirely, which decodes as an empty string.
func parseDuration(value string) (float64, error) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(value), "s")
	if trimmed == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || seconds < 0 {
		return 0, errors.New("invalid duration")
	}
	return seconds, nil
}

// StatusError is a non-200 provider answer; the body is never retained.
type StatusError struct{ Status int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("orderedroute: provider returned HTTP %d", e.Status)
}

// transportError hides request URLs, which carry coordinates, from error text.
type transportError struct{ cause error }

func (e *transportError) Error() string { return "orderedroute: provider request failed" }
func (e *transportError) Unwrap() error { return e.cause }

func unwrapURLError(err error) error {
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) {
			return err
		}
		err = urlErr.Err
	}
}
