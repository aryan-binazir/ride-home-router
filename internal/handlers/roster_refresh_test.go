package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
)

func TestRosterUpdateAndBulkLabelRefreshKeepFilteredPage(t *testing.T) {
	for _, kind := range []string{"participants", "drivers"} {
		t.Run(kind, func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			seedRosterReadPerson(t, store, kind, "AAA unrelated", "Oak Road")
			var id int64
			for i := range 51 {
				id = seedRosterReadPerson(t, store, kind, fmt.Sprintf("Match %03d", i), "Maple Avenue")
			}
			label, err := store.Labels().Create(t.Context(), &models.Label{Name: "Summer group"})
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"update", "add", "remove"} {
				form := url.Values{"search": {" mApLe "}, "offset": {"50"}, "label_id": {strconv.FormatInt(label.ID, 10)}}
				method, path := http.MethodPost, "/api/v1/"+kind+"/labels/"+operation
				var handle http.HandlerFunc
				if operation == "update" {
					method, path = http.MethodPut, "/api/v1/"+kind+"/"+strconv.FormatInt(id, 10)
					form.Set("name", "Match 050")
					form.Set("address", "Maple Avenue")
					form.Set("vehicle_capacity", "4")
					form.Set("label_ids", strconv.FormatInt(label.ID, 10))
					handle = h.HandleUpdateParticipant
					if kind == "drivers" {
						handle = h.HandleUpdateDriver
					}
				} else if kind == "participants" {
					form.Set("participant_ids", strconv.FormatInt(id, 10))
					handle = h.HandleAddParticipantsToLabel
					if operation == "remove" {
						handle = h.HandleRemoveParticipantsFromLabel
					}
				} else {
					form.Set("driver_ids", strconv.FormatInt(id, 10))
					handle = h.HandleAddDriversToLabel
					if operation == "remove" {
						handle = h.HandleRemoveDriversFromLabel
					}
				}
				req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")
				response := httptest.NewRecorder()
				handle(response, req)
				body := response.Body.String()
				if response.Code != http.StatusOK || !strings.Contains(body, "Match 050") || strings.Contains(body, "Match 049") || strings.Contains(body, "AAA unrelated") || !strings.Contains(body, `data-search-query="mApLe" data-offset="50"`) {
					t.Fatalf("%s refresh lost filtered page: %d %s", operation, response.Code, body)
				}
				if operation != "remove" && !strings.Contains(body, `>Summer group</span>`) {
					t.Fatalf("%s refresh lost label enrichment: %s", operation, body)
				}
				if operation == "remove" && strings.Contains(body, `>Summer group</span>`) {
					t.Fatalf("remove refresh retained removed label: %s", body)
				}
			}
		})
	}
}

func TestCompletedImportRefreshKeepsFilteredPage(t *testing.T) {
	for _, kind := range []importer.Kind{importer.KindParticipant, importer.KindDriver} {
		t.Run(string(kind), func(t *testing.T) {
			h, store := newImportTestHandler(t, &importTestGeocoder{})
			rosterKind := "participants"
			if kind == importer.KindDriver {
				rosterKind = "drivers"
			}
			seedRosterReadPerson(t, store, rosterKind, "AAA unrelated", "Oak Road")
			for i := range 51 {
				seedRosterReadPerson(t, store, rosterKind, fmt.Sprintf("Match %03d", i), "Maple Avenue")
			}
			upload := newImportPanelUploadRequest(t, "people.csv", "name,address,capacity\nMatch 051,Maple Lane,4\n", kind, "")
			uploadResponse := httptest.NewRecorder()
			h.HandleCreateImport(uploadResponse, upload)
			id := importPanelSessionID(t, uploadResponse.Body.String())
			mapping := url.Values{"column_0": {"name"}, "column_1": {"address"}}
			if kind == importer.KindDriver {
				mapping.Set("column_2", "capacity")
			}
			mappingResponse := httptest.NewRecorder()
			h.HandleImportSession(mappingResponse, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", mapping))
			waitForImportHTTPGeocoding(t, h, id)
			response := httptest.NewRecorder()
			h.HandleImportSession(response, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", url.Values{
				"selected": {"0"}, "search": {" mApLe "}, "offset": {"50"},
			}))
			body := response.Body.String()
			for _, want := range []string{"1 imported, 0 updated", "Match 050", "Match 051", `id="` + rosterKind + `-list"`, `hx-swap-oob="innerHTML"`, `data-search-query="mApLe" data-offset="50"`} {
				if response.Code != http.StatusOK || !strings.Contains(body, want) {
					t.Fatalf("import refresh missing %q: %d %s", want, response.Code, body)
				}
			}
			if strings.Contains(body, "AAA unrelated") || strings.Contains(body, "Match 049") {
				t.Fatalf("import refresh lost filtered window: %s", body)
			}
		})
	}
}
