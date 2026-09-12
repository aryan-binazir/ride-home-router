package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
)

func TestDesktopRosterGeocodeFailureShowsSafeToast(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(kind+method, func(t *testing.T) {
				h, _ := newTestManagementHandler(t)
				path := "/api/v1/" + kind + "s"
				if method == http.MethodPut {
					created := rosterRequest(h, kind, http.MethodPost, path, false)
					var person struct {
						ID int64 `json:"id"`
					}
					if err := json.Unmarshal(created.Body.Bytes(), &person); err != nil || person.ID == 0 {
						t.Fatalf("seed: %s, %v", created.Body.String(), err)
					}
					path += "/" + strconv.FormatInt(person.ID, 10)
				}
				h.Geocoder = stubGeocoder{err: errors.New("private-provider-data")}
				response := rosterRequest(h, kind, method, path, true)
				var trigger struct {
					ShowToast htmxToast `json:"showToast"`
				}
				if err := json.Unmarshal([]byte(response.Header().Get("HX-Trigger")), &trigger); err != nil {
					t.Fatalf("missing visible error toast: %v; status=%d", err, response.Code)
				}
				if response.Code < 400 || trigger.ShowToast.Type != "error" || !strings.Contains(strings.ToLower(trigger.ShowToast.Message), "address") || strings.Contains(trigger.ShowToast.Message, "private-provider-data") {
					t.Fatalf("response=%d toast=%+v", response.Code, trigger.ShowToast)
				}
			})
		}
	}
}

func rosterRequest(h *Handler, kind, method, path string, htmx bool) *httptest.ResponseRecorder {
	values := url.Values{"name": {"Test Person"}, "address": {"1 Original Road"}, "vehicle_capacity": {"4"}}
	if method == http.MethodPut {
		values.Set("address", "2 Changed Road")
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Roster adapters use the HTMX request header to select form decoding.
	request.Header.Set("HX-Request", "true")
	if !htmx {
		request = httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(`{"name":"Test Person","address":"1 Original Road","vehicle_capacity":4}`))
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	if kind == "participant" {
		if method == http.MethodPost {
			h.HandleCreateParticipant(response, request)
		} else {
			h.HandleUpdateParticipant(response, request)
		}
	} else {
		if method == http.MethodPost {
			h.HandleCreateDriver(response, request)
		} else {
			h.HandleUpdateDriver(response, request)
		}
	}
	return response
}

// Fault injection stays at the database boundary; writes use the real store.
type rosterRefreshFailureStore struct {
	database.DataStore
	participants database.ParticipantRepository
	drivers      database.DriverRepository
	labels       database.LabelRepository
}

func (s rosterRefreshFailureStore) Participants() database.ParticipantRepository {
	return s.participants
}
func (s rosterRefreshFailureStore) Drivers() database.DriverRepository { return s.drivers }
func (s rosterRefreshFailureStore) Labels() database.LabelRepository   { return s.labels }

type failedParticipantList struct{ database.ParticipantRepository }

func (s failedParticipantList) List(ctx context.Context, search string) ([]models.Participant, error) {
	rows, err := s.ParticipantRepository.List(ctx, search)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	return nil, errors.New("post-write list failure")
}

type failedDriverList struct{ database.DriverRepository }

func (s failedDriverList) List(ctx context.Context, search string) ([]models.Driver, error) {
	rows, err := s.DriverRepository.List(ctx, search)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	return nil, errors.New("post-write list failure")
}

func TestDesktopRosterCreateAcknowledgesCommitWhenRefreshFails(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		for _, failure := range []string{"list", "enrichment"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				h, store := newTestManagementHandler(t)
				faults := rosterRefreshFailureStore{DataStore: store, participants: store.Participants(), drivers: store.Drivers(), labels: store.Labels()}
				if failure == "list" {
					faults.participants = failedParticipantList{store.Participants()}
					faults.drivers = failedDriverList{store.Drivers()}
				} else {
					faults.labels = failedRosterLabels{store.Labels()}
				}
				h.DB = faults
				response := rosterRequest(h, kind, http.MethodPost, "/api/v1/"+kind+"s", true)
				var trigger map[string]json.RawMessage
				if err := json.Unmarshal([]byte(response.Header().Get("HX-Trigger")), &trigger); err != nil {
					t.Fatalf("no acknowledgement: %v", err)
				}
				var toast htmxToast
				if err := json.Unmarshal(trigger["showToast"], &toast); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusNoContent || string(trigger[kind+"Created"]) != "true" || !strings.Contains(toast.Message, "saved") || !strings.Contains(toast.Message, "Refresh") {
					t.Fatalf("response=%d trigger=%s", response.Code, response.Header().Get("HX-Trigger"))
				}
				h.DB = store
				request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/"+kind+"s", nil)
				listed := httptest.NewRecorder()
				if kind == "participant" {
					h.HandleListParticipants(listed, request)
				} else {
					h.HandleListDrivers(listed, request)
				}
				var listing struct {
					Total int `json:"total"`
				}
				if err := json.Unmarshal(listed.Body.Bytes(), &listing); err != nil || listing.Total != 1 {
					t.Fatalf("saved roster=%s err=%v", listed.Body.String(), err)
				}
			})
		}
	}
}

type failedRosterLabels struct{ database.LabelRepository }

func (s failedRosterLabels) List(context.Context) ([]models.Label, error) {
	return nil, errors.New("post-write label failure")
}

func (s failedRosterLabels) ListLabelsForParticipant(context.Context, int64) ([]models.Label, error) {
	return nil, errors.New("post-write label failure")
}

func (s failedRosterLabels) ListLabelsForDriver(context.Context, int64) ([]models.Label, error) {
	return nil, errors.New("post-write label failure")
}

func TestJSONRosterCreateAcknowledgesCommitWhenLabelReadFails(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		t.Run(kind, func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			h.DB = rosterRefreshFailureStore{DataStore: store, participants: store.Participants(), drivers: store.Drivers(), labels: failedRosterLabels{store.Labels()}}
			response := rosterRequest(h, kind, http.MethodPost, "/api/v1/"+kind+"s", false)
			var person struct {
				ID       int64   `json:"id"`
				LabelIDs []int64 `json:"label_ids"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &person); err != nil || response.Code != http.StatusCreated || person.ID == 0 || person.LabelIDs == nil {
				t.Fatalf("create response=%d %s err=%v", response.Code, response.Body.String(), err)
			}
		})
	}
}

func TestDesktopDuplicateRosterCreateRejected(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := newTestManagementHandler(t)
			first := rosterRequest(h, kind, http.MethodPost, "/api/v1/"+kind+"s", true)
			if first.Code >= 400 {
				t.Fatalf("first create: %d %s", first.Code, first.Body.String())
			}
			second := rosterRequest(h, kind, http.MethodPost, "/api/v1/"+kind+"s", true)
			if second.Code != 409 || !strings.Contains(second.Header().Get("HX-Trigger"), "already in the roster") {
				t.Fatalf("second create: %d %s", second.Code, second.Header())
			}
		})
	}
}
