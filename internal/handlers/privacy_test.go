package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
	"github.com/aryan-binazir/ride-home-router/internal/logtest"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

const (
	privateAddressSentinel = "9137 Rawarg Sentinel Way"
	privateErrorSentinel   = "8246 Errstring Sentinel Lane"
)

type privacyStore struct {
	database.DataStore
	participants database.ParticipantRepository
	labels       database.LabelRepository
}

func (s privacyStore) Participants() database.ParticipantRepository { return s.participants }
func (s privacyStore) Labels() database.LabelRepository             { return s.labels }

type privacyParticipantRepository struct {
	database.ParticipantRepository
}

func (privacyParticipantRepository) GetByID(context.Context, int64) (*models.Participant, error) {
	return &models.Participant{ID: 42, Address: "Previous Address", Lat: 35.9, Lng: -79.1}, nil
}

type privacyLabelRepository struct {
	database.LabelRepository
}

func (privacyLabelRepository) GetByIDs(context.Context, []int64) ([]models.Label, error) {
	return nil, nil
}

func TestActivityLocationLogsInvalidPathAsOneLine(t *testing.T) {
	var logs bytes.Buffer
	logtest.Capture(t, &logs)
	h := &Handler{}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/activity-locations/bad%0D%0Avalue", nil)
	w := httptest.NewRecorder()
	h.HandleGetActivityLocation(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
	output := logs.String()
	if strings.Count(output, "\n") != 1 || strings.ContainsRune(output, '\r') ||
		!strings.Contains(output, `method=GET`) || !strings.Contains(output, `path="/api/v1/activity-locations/bad\r\nvalue"`) {
		t.Fatalf("expected one structured log with an escaped path, got %q", output)
	}
}

func TestHandlerLogsDoNotContainAddressesOrAddressQueries(t *testing.T) {
	var logs bytes.Buffer
	logtest.Capture(t, &logs)

	handler := &Handler{
		DB: privacyStore{
			participants: privacyParticipantRepository{},
			labels:       privacyLabelRepository{},
		},
		Geocoder: stubGeocoder{err: errors.New(privateErrorSentinel)},
	}

	requests := []*http.Request{
		httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/participants", strings.NewReader(`{"name":"Child","address":"`+privateAddressSentinel+`"}`)),
		httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/participants/42", strings.NewReader(`{"name":"Child","address":"`+privateAddressSentinel+`"}`)),
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/address-search?address="+url.QueryEscape(privateAddressSentinel), nil),
	}
	requests[0].Header.Set(httpx.HeaderContentType, httpx.MediaTypeJSON)
	requests[1].Header.Set(httpx.HeaderContentType, httpx.MediaTypeJSON)
	requests[2].Header.Set(httpx.HeaderHXRequest, httpx.HTMXTrue)

	handler.HandleCreateParticipant(httptest.NewRecorder(), requests[0])
	handler.HandleUpdateParticipant(httptest.NewRecorder(), requests[1])
	handler.HandleAddressSearch(httptest.NewRecorder(), requests[2])

	output := logs.String()
	for _, private := range []string{
		privateAddressSentinel,
		privateErrorSentinel,
		strings.ToLower(privateAddressSentinel),
		strings.ToUpper(privateAddressSentinel),
		url.QueryEscape(privateAddressSentinel),
		url.PathEscape(privateAddressSentinel),
		strconv.Quote(privateAddressSentinel),
		"Rawarg Sentinel",
		"Errstring Sentinel",
	} {
		if strings.Contains(output, private) {
			t.Fatalf("logs contain private value %q: %s", private, output)
		}
	}
	for _, want := range []string{
		"method=POST path=/api/v1/participants",
		"method=PUT route=/api/v1/participants/{id}",
		"method=GET path=/api/v1/address-search",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs missing %q: %s", want, output)
		}
	}
}

func TestRosterLogsDoNotContainNames(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	const privateName = "PrivateNameSentinelFalcon"
	var logs bytes.Buffer
	logtest.Capture(t, &logs)
	for _, tc := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/v1/participants", h.HandleCreateParticipant},
		{"/api/v1/drivers", h.HandleCreateDriver},
		{"/api/v1/activity-locations", h.HandleCreateActivityLocation},
		{"/api/v1/org-vehicles", h.HandleCreateOrgVehicle},
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, tc.path, strings.NewReader(url.Values{"name": {privateName}, "address": {"123 Test Street"}, "capacity": {"4"}, "vehicle_capacity": {"4"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")

		tc.handle(httptest.NewRecorder(), r)
		if !strings.Contains(logs.String(), "method=POST path="+tc.path) {
			t.Fatalf("logs missing create request for %s: %s", tc.path, logs.String())
		}
	}
	if strings.Contains(logs.String(), privateName) {
		t.Fatalf("name leaked: %s", logs.String())
	}
}
