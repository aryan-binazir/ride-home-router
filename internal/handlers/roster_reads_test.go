package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"strconv"
	"strings"
	"testing"
)

func seedRosterReadPerson(t *testing.T, store *postgres.Store, kind, name, address string) int64 {
	t.Helper()
	if kind == "participants" {
		p, err := store.Participants().Create(t.Context(), &models.Participant{Name: name, Address: address})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	d, err := store.Drivers().Create(t.Context(), &models.Driver{Name: name, Address: address, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestRosterReadPagingAndJSONPolicy(t *testing.T) {
	for _, kind := range []string{"participants", "drivers"} {
		t.Run(kind, func(t *testing.T) {
			h, store := newTestPageHandler(t)
			seedRosterReadPerson(t, store, kind, "AAA unrelated", "Oak Road")
			for i := range 101 {
				seedRosterReadPerson(t, store, kind, fmt.Sprintf("Match %03d", i), "Maple Avenue")
			}
			list, page := h.HandleListParticipants, h.HandleParticipantsPage
			if kind == "drivers" {
				list, page = h.HandleListDrivers, h.HandleDriversPage
			}
			for _, endpoint := range []struct {
				path   string
				handle http.HandlerFunc
			}{
				{"/api/v1/" + kind, list}, {"/" + kind, page},
			} {
				for _, offset := range []struct {
					requested, actual, first, last string
					count                          int
				}{
					{"50", "50", "Match 050", "Match 099", 50},
					{"999", "100", "Match 100", "Match 100", 1},
					{"-1", "0", "Match 000", "Match 049", 50},
					{"invalid", "0", "Match 000", "Match 049", 50},
				} {
					req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.path+"?search=+mApLe+&offset="+offset.requested, nil)
					req.Header.Set("HX-Request", "true")
					response := httptest.NewRecorder()
					endpoint.handle(response, req)
					body := response.Body.String()
					if response.Code != http.StatusOK || !strings.Contains(body, offset.first) || !strings.Contains(body, offset.last) || strings.Contains(body, "AAA unrelated") {
						t.Fatalf("%s offset=%s: status=%d, wrong rows", endpoint.path, offset.requested, response.Code)
					}
					if !strings.Contains(body, `data-search-query="mApLe" data-offset="`+offset.actual+`"`) || strings.Count(body, `data-row="`+strings.TrimSuffix(kind, "s")+`-`) != offset.count {
						t.Fatalf("%s offset=%s: wrong paging metadata or row count", endpoint.path, offset.requested)
					}
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+kind+"?search=+mApLe+&offset=50", nil)
			response := httptest.NewRecorder()
			list(response, req)
			var payload struct {
				Participants []ParticipantResponse `json:"participants"`
				Drivers      []DriverResponse      `json:"drivers"`
				Total        int                   `json:"total"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK || payload.Total != 101 || len(payload.Participants)+len(payload.Drivers) != 101 {
				t.Fatalf("JSON must remain unpaged: %d %s, %v", response.Code, response.Body.String(), err)
			}
			for _, p := range payload.Participants {
				if p.LabelIDs == nil {
					t.Fatal("participant label_ids must be []")
				}
			}
			for _, d := range payload.Drivers {
				if d.LabelIDs == nil {
					t.Fatal("driver label_ids must be []")
				}
			}
			empty := httptest.NewRecorder()
			list(empty, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+kind+"?search=absent", nil))
			if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), `"`+kind+`":[]`) || !strings.Contains(empty.Body.String(), `"total":0`) {
				t.Fatalf("empty JSON shape = %d %s", empty.Code, empty.Body.String())
			}
		})
	}
}

func TestDeletedRosterReadPreservesSourcePagingAndLabels(t *testing.T) {
	for _, kind := range []string{"participants", "drivers"} {
		t.Run(kind, func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			seedRosterReadPerson(t, store, kind, "Live only", "Live Road")
			label, err := store.Labels().Create(t.Context(), &models.Label{Name: "Retained label"})
			if err != nil {
				t.Fatal(err)
			}
			for i := range 101 {
				id := seedRosterReadPerson(t, store, kind, fmt.Sprintf("Deleted %03d", i), "Deleted Road")
				if kind == "participants" {
					err = store.Labels().AddLabelToParticipants(t.Context(), label.ID, []int64{id})
					if err == nil {
						err = store.Participants().Delete(t.Context(), id)
					}
				} else {
					err = store.Labels().AddLabelToDrivers(t.Context(), label.ID, []int64{id})
					if err == nil {
						err = store.Drivers().Delete(t.Context(), id)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			list := h.HandleListDeletedParticipants
			if kind == "drivers" {
				list = h.HandleListDeletedDrivers
			}
			for _, offset := range []int{50, 999} {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/"+kind+"/deleted?search=missing&offset="+strconv.Itoa(offset), nil)
				req.Header.Set("HX-Request", "true")
				response := httptest.NewRecorder()
				list(response, req)
				body := response.Body.String()
				if response.Code != http.StatusOK || strings.Contains(body, "Live only") || !strings.Contains(body, "Retained label") {
					t.Fatalf("deleted source and labels: %d %s", response.Code, body)
				}
				wantFirst, wantLast, wantCount := "Deleted 050", "Deleted 001", 50
				if offset == 999 {
					wantFirst, wantLast, wantCount = "Deleted 000", "Deleted 000", 1
				}
				if !strings.Contains(body, wantFirst) || !strings.Contains(body, wantLast) || strings.Count(body, `id="deleted-`+strings.TrimSuffix(kind, "s")+`-`) != wantCount {
					t.Fatalf("deleted offset=%d: wrong window", offset)
				}
				if !strings.Contains(body, `hx-target="#`+kind+`-deleted"`) || !strings.Contains(body, "/api/v1/"+kind+"/deleted?offset=50&amp;search=missing") && offset == 999 {
					t.Fatalf("deleted paging target or previous page lost: %s", body)
				}
				if offset == 50 && !strings.Contains(body, "/api/v1/"+kind+"/deleted?offset=100&amp;search=missing") {
					t.Fatalf("deleted next page lost: %s", body)
				}
			}
		})
	}
}
