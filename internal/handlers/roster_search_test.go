package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
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
