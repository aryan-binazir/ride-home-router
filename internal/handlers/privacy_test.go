package handlers

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
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

func TestHandlerLogsDoNotContainAddressesOrAddressQueries(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

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
	for _, want := range []string{"POST /api/v1/participants", "PUT /api/v1/participants", "GET /api/v1/address-search"} {
		if !strings.Contains(output, want) {
			t.Fatalf("logs missing %q: %s", want, output)
		}
	}
}

func TestRosterLogsDoNotContainNames(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	const privateName = "PrivateNameSentinelFalcon"
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	for _, tc := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/v1/participants", h.HandleCreateParticipant},
		{"/api/v1/drivers", h.HandleCreateDriver},
		{"/api/v1/activity-locations", h.HandleCreateActivityLocation},
		{"/api/v1/org-vehicles", h.HandleCreateOrgVehicle},
		{"/m/people/participants/new", h.HandleMobileParticipantForm},
		{"/m/people/drivers/new", h.HandleMobileDriverForm},
		{"/m/places/locations/new", h.HandleMobileLocationForm},
		{"/m/places/vans/new", h.HandleMobileVanForm},
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, tc.path, strings.NewReader(url.Values{"name": {privateName}, "address": {"123 Test Street"}, "capacity": {"4"}, "vehicle_capacity": {"4"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if !strings.HasPrefix(tc.path, "/m/") {
			r.Header.Set("HX-Request", "true")
		}
		tc.handle(httptest.NewRecorder(), r)
	}
	if strings.Contains(logs.String(), privateName) {
		t.Fatalf("name leaked: %s", logs.String())
	}
}
