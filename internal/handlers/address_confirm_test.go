package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestRosterEditorRecordsAddressMatch(t *testing.T) {
	h, store := newTestManagementHandler(t)
	ctx := context.Background()
	editor := rosterEditor{db: store, geocoder: stubGeocoder{result: &geocoding.GeocodingResult{
		Coords: models.Coordinates{Lat: 35.7, Lng: -78.6}, FormattedAddress: "12 Oak Street, Raleigh, NC 27601", Guessed: true,
	}}}
	created, err := editor.createParticipant(ctx, participantEdit{Name: "Rider", Address: "12 Oak St Raliegh"})
	if err != nil {
		t.Fatal(err)
	}
	if created.AddressMatch != models.AddressMatchGuessed || created.MatchedAddress != "12 Oak Street, Raleigh, NC 27601" {
		t.Fatalf("created = %#v", created)
	}
	// Editing without changing the address keeps the status.
	same, err := editor.updateParticipant(ctx, created, participantEdit{Name: "Renamed", Address: created.Address})
	if err != nil || same.AddressMatch != models.AddressMatchGuessed {
		t.Fatalf("same-address update = %#v, %v", same, err)
	}
	// A new address is re-evaluated from the fresh lookup.
	editor.geocoder = h.Geocoder
	fixed, err := editor.updateParticipant(ctx, same, participantEdit{Name: "Renamed", Address: "1 Verified Way"})
	if err != nil || fixed.AddressMatch != models.AddressMatchVerified {
		t.Fatalf("new-address update = %#v, %v", fixed, err)
	}
}

func TestHandleConfirmParticipantAddress(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	ctx := context.Background()
	participant, err := store.Participants().Create(ctx, &models.Participant{
		Name: "Rider", Address: "12 Oak St Raliegh", Lat: 35.7, Lng: -78.6,
		MatchedAddress: "12 Oak Street, Raleigh, NC 27601", AddressMatch: models.AddressMatchGuessed,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/participants/" + strconv.FormatInt(participant.ID, 10) + AddressConfirmSuffix

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(""))
	req.Header.Set("HX-Request", "true")
	handler.HandleConfirmParticipantAddress(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `id="participants-tbody"`) {
		t.Fatalf("HTMX confirm status = %d body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "12 Oak Street, Raleigh, NC 27601") || strings.Contains(rr.Body.String(), "Raliegh") {
		t.Fatalf("roster should show the matched address: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `class="address-marker address-marker-confirmed"`) || !strings.Contains(rr.Body.String(), `data-row="participant-`+strconv.FormatInt(participant.ID, 10)+`"`) {
		t.Fatalf("roster row should carry a confirmed marker: %s", rr.Body.String())
	}
	got, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil || got.Address != "12 Oak Street, Raleigh, NC 27601" || got.AddressMatch != models.AddressMatchConfirmed || got.Lat != 35.7 {
		t.Fatalf("confirmed participant = %#v, %v", got, err)
	}

	rr = httptest.NewRecorder()
	handler.HandleConfirmParticipantAddress(rr, httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader("")))
	var response ParticipantResponse
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &response) != nil || response.AddressMatch != models.AddressMatchConfirmed {
		t.Fatalf("JSON confirm status = %d body = %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.HandleConfirmParticipantAddress(rr, httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/participants/999999"+AddressConfirmSuffix, strings.NewReader("")))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing participant status = %d body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleConfirmDriverAddress(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	ctx := context.Background()
	driver, err := store.Drivers().Create(ctx, &models.Driver{
		Name: "Driver", Address: "5 Elm", Lat: 35.7, Lng: -78.6, VehicleCapacity: 4,
		MatchedAddress: "5 Elm St, Durham, NC 27701", AddressMatch: models.AddressMatchGuessed,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/drivers/" + strconv.FormatInt(driver.ID, 10) + AddressConfirmSuffix
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(""))
	req.Header.Set("HX-Request", "true")
	handler.HandleConfirmDriverAddress(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "5 Elm St, Durham, NC 27701") {
		t.Fatalf("HTMX confirm status = %d body = %s", rr.Code, rr.Body.String())
	}
	got, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || got.Address != "5 Elm St, Durham, NC 27701" || got.AddressMatch != models.AddressMatchConfirmed || got.VehicleCapacity != 4 {
		t.Fatalf("confirmed driver = %#v, %v", got, err)
	}
	rr = httptest.NewRecorder()
	handler.HandleConfirmDriverAddress(rr, httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/drivers/999999"+AddressConfirmSuffix, strings.NewReader("")))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing driver status = %d", rr.Code)
	}
}

type guessingImportGeocoder struct {
	geocoding.Geocoder
	mu sync.Mutex
}

func (g *guessingImportGeocoder) GeocodeWithRetry(_ context.Context, address string, _ int) (*geocoding.GeocodingResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return &geocoding.GeocodingResult{
		Coords: models.Coordinates{Lat: 35.7, Lng: -78.6}, FormattedAddress: "Matched " + address,
		Guessed: strings.Contains(address, "Raliegh"),
	}, nil
}

func TestImportPanelFlagsGuessedAddresses(t *testing.T) {
	handler, db := newImportTestHandler(t, &guessingImportGeocoder{})
	upload := newImportPanelUploadRequest(t, "riders.csv", "name,address\nAlex,12 Oak St Raliegh\nBlair,2 Main St\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	id := importPanelSessionID(t, uploadRecorder.Body.String())
	mappingRecorder := httptest.NewRecorder()
	handler.HandleImportSession(mappingRecorder, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	waitForImportHTTPGeocoding(t, handler, id)

	previewRecorder := httptest.NewRecorder()
	handler.HandleImportSession(previewRecorder, newImportPanelRequest(http.MethodGet, "/api/v1/imports/"+id+"?view=panel"))
	preview := previewRecorder.Body.String()
	if !strings.Contains(preview, "Google couldn&#39;t find this exactly. Matched to: Matched 12 Oak St Raliegh. Check this.") || !strings.Contains(preview, "import-row-warning") {
		t.Fatalf("preview should flag the guessed row: %s", preview)
	}
	if strings.Count(preview, "Check this.") != 1 {
		t.Fatalf("only the guessed row should carry the note: %s", preview)
	}

	commitRecorder := httptest.NewRecorder()
	handler.HandleImportSession(commitRecorder, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", url.Values{"selected": {"0", "1"}}))
	result := commitRecorder.Body.String()
	want := "We couldn&#39;t confirm 1 address exactly, so we used Google&#39;s closest match. Look for the red marker next to that name and check it. Click the marker to see what we matched and confirm it or fix it."
	if !strings.Contains(result, want) {
		t.Fatalf("result should warn about the guessed address: %s", result)
	}
	participants, err := db.Participants().List(context.Background(), "")
	if err != nil || len(participants) != 2 {
		t.Fatalf("participants = %#v, %v", participants, err)
	}
	if !strings.Contains(result, `class="address-marker address-marker-guessed"`) || !strings.Contains(result, `data-matched="Matched 12 Oak St Raliegh"`) {
		t.Fatalf("refreshed roster should flag the guessed row: %s", result)
	}
	for _, p := range participants {
		wantMatch := models.AddressMatchVerified
		if p.Name == "Alex" {
			wantMatch = models.AddressMatchGuessed
		}
		if p.AddressMatch != wantMatch || p.MatchedAddress != "Matched "+p.Address {
			t.Fatalf("participant %s = %#v", p.Name, p)
		}
	}
}

func TestImportGuessedWarningIsEmptyWhenAllVerified(t *testing.T) {
	if got := importGuessedWarning(importer.CommitResult{Created: 3}); got != "" {
		t.Fatalf("importGuessedWarning() = %q, want empty", got)
	}
	if got := importGuessedWarning(importer.CommitResult{Created: 3, Guessed: 3}); !strings.HasPrefix(got, "We couldn't confirm 3 addresses exactly") {
		t.Fatalf("importGuessedWarning() = %q", got)
	}
}
