package geocoding

import (
	"context"
	"errors"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"time"
)

// GeocodingResult contains the result of a geocoding operation
type GeocodingResult struct {
	Coords           models.Coordinates
	DisplayName      string
	FormattedAddress string
}

// Label returns the address text shown in search suggestions.
func (r GeocodingResult) Label() string {
	if strings.TrimSpace(r.FormattedAddress) != "" {
		return r.FormattedAddress
	}
	return r.DisplayName
}

// Geocoder provides address-to-coordinates conversion
type Geocoder interface {
	Geocode(ctx context.Context, address string) (*GeocodingResult, error)
	GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*GeocodingResult, error)
	Search(ctx context.Context, query string, limit int) ([]GeocodingResult, error)
}

var ErrNoGeocodingResults = errors.New("geocoding: no results found")

// ErrGeocodingFailed is returned when an address cannot be geocoded
type ErrGeocodingFailed struct {
	address    string
	Reason     string
	Cause      error
	HTTPStatus int
	RetryAfter time.Duration
	// Temporary marks provider or credential conditions worth retrying later.
	Temporary bool
}

func (e *ErrGeocodingFailed) Error() string {
	return "geocoding failed: " + e.Reason
}

func (e *ErrGeocodingFailed) Unwrap() error {
	return e.Cause
}

// Retryable distinguishes temporary provider or pacing failures from invalid addresses.
func (e *ErrGeocodingFailed) Retryable() bool {
	if e.Temporary {
		return true
	}
	if _, ok := errors.AsType[*providerTransportError](e.Cause); ok {
		return true
	}
	return isRetryableStatus(e.HTTPStatus)
}

// CooldownError reports an excessive persisted deadline that was capped for recovery.
type CooldownError struct{}

func (*CooldownError) Error() string {
	return "Address lookup is temporarily unavailable. Try again later."
}

// RateGate propagates provider cooldowns across application instances.
type RateGate interface {
	Wait(context.Context) error
	Defer(context.Context, time.Duration) error
}

const (
	geocoderClientTimeout = 10 * time.Second
	maxAttempts           = 3
	maxRetryAfter         = 15 * time.Minute
)

func geocodeWithRetry(ctx context.Context, address string, maxRetries int, geocode func(context.Context, string) (*GeocodingResult, error)) (*GeocodingResult, error) {
	var lastErr error
	started := time.Now()
	attempts := max(1, min(maxRetries, maxAttempts))

	for i := range attempts {
		result, err := geocode(ctx, address)
		if err == nil {
			log.Printf("[GEOCODING] Retry operation outcome=success attempts=%d duration=%s", i+1, time.Since(started).Round(time.Millisecond))
			return result, nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		backoff, retryable := retryDelay(err, i)
		if !retryable {
			break
		}
		if i < attempts-1 {
			log.Printf("[GEOCODING] Retry %d/%d: backoff=%v", i+1, attempts, backoff)
			if err := waitForRetry(ctx, backoff); err != nil {
				return nil, err
			}
		}
	}

	log.Printf("[ERROR] Retry operation outcome=failed retries=%d duration=%s", attempts, time.Since(started).Round(time.Millisecond))
	return nil, lastErr
}

// providerTransportError hides request URLs (which carry addresses and keys) from error text.
type providerTransportError struct {
	Cause error
}

func newProviderTransportError(cause error) *providerTransportError {
	for {
		var urlErr *url.Error
		if !errors.As(cause, &urlErr) {
			break
		}
		cause = urlErr.Err
	}
	return &providerTransportError{Cause: cause}
}

func (e *providerTransportError) Error() string {
	return e.Cause.Error()
}

func (e *providerTransportError) Unwrap() error {
	return e.Cause
}

// bodyReader records transport failures that surface while decoding a response body.
type bodyReader struct {
	io.Reader
	Err error
}

func (r *bodyReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.Err = err
	}
	return n, err
}

func retryDelay(err error, attempt int) (time.Duration, bool) {
	if _, ok := errors.AsType[*CooldownError](err); ok {
		return 0, false
	}
	var geocodingErr *ErrGeocodingFailed
	if !errors.As(err, &geocodingErr) {
		return 0, false
	}

	if !geocodingErr.Retryable() {
		return 0, false
	}

	base := time.Second << attempt
	//nolint:gosec // G404: retry jitter does not need cryptographic randomness.
	delay := max(base+time.Duration(rand.Int64N(int64(base/2)+1)), geocodingErr.RetryAfter)
	return max(delay, time.Second), true
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 0 {
			if seconds > int64(maxRetryAfter/time.Second) {
				return maxRetryAfter
			}
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return min(max(time.Until(when), 0), maxRetryAfter)
}
