package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
)

func TestRosterSearchRendersSQLMatches(t *testing.T) {
	h, store := newTestPageHandler(t)
	for _, name := range []string{"Zelda", "Alice"} {
		address, addressName := "456 Oak Road", ""
		if name == "Zelda" {
			address, addressName = "123 Maple Avenue", "Community Center"
		}
		if _, err := store.Participants().Create(t.Context(), &models.Participant{Name: name, Address: address, AddressName: addressName}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: name, Address: address, AddressName: addressName}); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/v1/participants", h.HandleListParticipants},
		{"/api/v1/drivers", h.HandleListDrivers},
		{"/participants", h.HandleParticipantsPage},
		{"/drivers", h.HandleDriversPage},
	} {
		for _, search := range []string{"  mApLe  ", "  cEnTeR  ", "missing", "   "} {
			t.Run(endpoint.path+"/"+search, func(t *testing.T) {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.path+"?search="+url.QueryEscape(search), nil)
				req.Header.Set("HX-Request", "true")
				response := httptest.NewRecorder()
				endpoint.handle(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("status = %d: %s", response.Code, response.Body.String())
				}
				body := response.Body.String()
				if got, want := strings.Contains(body, "Zelda"), search != "missing"; got != want {
					t.Fatalf("matching row present = %v; want %v", got, want)
				}
				if got, want := strings.Contains(body, "Alice"), strings.TrimSpace(search) == ""; got != want {
					t.Fatalf("other row present = %v; want %v", got, want)
				}
			})
		}
	}
}

func TestPageRosterPreservesSQLResultsAndSearch(t *testing.T) {
	items := make([]models.Participant, 101)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/participants?search=+Maple+&offset=50", nil)
	rows, page := pageRosterParticipants(req, items)
	if len(rows) != 50 || page.Offset != 50 || page.Search != "Maple" {
		t.Fatalf("rows = %d, page = %#v; want unfiltered page and trimmed search", len(rows), page)
	}
	if page.NextURL != "/api/v1/participants?offset=100&search=Maple" || page.PreviousURL != "/api/v1/participants?offset=0&search=Maple" {
		t.Fatalf("pagination URLs = %#v", page)
	}
}

// Roster refreshes after an edit carry the active search term, so the
// re-rendered page must stay filtered instead of showing the full roster.
func TestRosterRefreshAfterUpdateKeepsSearch(t *testing.T) {
	h, store := newTestPageHandler(t)
	zelda, err := store.Participants().Create(t.Context(), &models.Participant{Name: "Zelda", Address: "123 Maple Avenue", AddressName: "Community Center"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Participants().Create(t.Context(), &models.Participant{Name: "Alice", Address: "456 Oak Road"}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"Zelda"}, "address": {"123 Maple Avenue"}, "address_name": {"Community Center"}, "search": {" maple "}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/participants/"+strconv.FormatInt(zelda.ID, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	h.HandleUpdateParticipant(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Zelda") || strings.Contains(body, "Alice") {
		t.Fatalf("refreshed roster ignored search: %s", body)
	}
}
