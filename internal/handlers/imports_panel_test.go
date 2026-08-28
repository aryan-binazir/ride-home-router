package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

func TestImportPanelFlowRendersFragmentsAndRefreshesRoster(t *testing.T) {
	handler, db := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\nBlair,2 Main St\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	assertPanelFragment(t, uploadRecorder)
	mappingBody := uploadRecorder.Body.String()
	for _, want := range []string{`name="column_0"`, `name="column_1"`, `value="name" selected`, `value="address" selected`, "Continue"} {
		if !strings.Contains(mappingBody, want) {
			t.Fatalf("mapping fragment missing %q: %s", want, mappingBody)
		}
	}
	if strings.Contains(mappingBody, "Passenger capacity") {
		t.Fatal("participant mapping should not offer the driver capacity field")
	}
	id := importPanelSessionID(t, mappingBody)

	mapping := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{
		"column_0": {"name"}, "column_1": {"address"},
	})
	mappingRecorder := httptest.NewRecorder()
	handler.HandleImportSession(mappingRecorder, mapping)
	assertPanelFragment(t, mappingRecorder)
	previewBody := mappingRecorder.Body.String()
	for _, want := range []string{"Looking up addresses…", "<progress", `hx-trigger="every 2s"`, "2 of 2 rows selected", "Import 2 rows", "disabled"} {
		if !strings.Contains(previewBody, want) {
			t.Fatalf("preview fragment missing %q: %s", want, previewBody)
		}
	}

	waitForImportHTTPGeocoding(t, handler, id)

	pollRecorder := httptest.NewRecorder()
	handler.HandleImportSession(pollRecorder, newImportPanelRequest(http.MethodGet, "/api/v1/imports/"+id+"?view=panel"))
	assertPanelFragment(t, pollRecorder)
	polledBody := pollRecorder.Body.String()
	if strings.Contains(polledBody, "Looking up addresses…") || strings.Contains(polledBody, "every 2s") {
		t.Fatalf("finished preview should stop polling: %s", polledBody)
	}
	if !strings.Contains(polledBody, "Import 2 rows") || strings.Contains(polledBody, "disabled") {
		t.Fatalf("finished preview should enable the commit button: %s", polledBody)
	}

	selection := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/selection?view=panel", url.Values{"selected": {"0"}})
	selectionRecorder := httptest.NewRecorder()
	handler.HandleImportSession(selectionRecorder, selection)
	assertPanelFragment(t, selectionRecorder)
	if body := selectionRecorder.Body.String(); !strings.Contains(body, "1 of 2 rows selected") || !strings.Contains(body, "Import 1 row") {
		t.Fatalf("commit bar = %s", body)
	}

	commit := newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", url.Values{"selected": {"0"}})
	commitRecorder := httptest.NewRecorder()
	handler.HandleImportSession(commitRecorder, commit)
	assertPanelFragment(t, commitRecorder)
	resultBody := commitRecorder.Body.String()
	if !strings.Contains(resultBody, "1 imported, 0 skipped as duplicates") {
		t.Fatalf("result fragment = %s", resultBody)
	}
	if !strings.Contains(resultBody, `id="participants-list"`) || !strings.Contains(resultBody, `hx-swap-oob="true"`) {
		t.Fatalf("result fragment should refresh the roster out of band: %s", resultBody)
	}
	if !strings.Contains(resultBody, "Alex") || strings.Contains(resultBody, "Blair") {
		t.Fatalf("refreshed roster should list only the committed row: %s", resultBody)
	}

	participants, err := db.Participants().List(context.Background(), "")
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(participants) != 1 || participants[0].Name != "Alex" {
		t.Fatalf("committed participants = %#v", participants)
	}
}

func TestImportPanelPreviewRendersRowStates(t *testing.T) {
	handler, db := newImportTestHandler(t, &importTestGeocoder{})
	if _, err := db.Participants().Create(context.Background(), &models.Participant{Name: "Dana", Address: "4 Main St", Lat: 40, Lng: -73}); err != nil {
		t.Fatalf("seed participant: %v", err)
	}

	contents := "name,address,lat,lng\n" +
		"Alex,1 Main St,,\n" +
		"Blair,2 Main St,33.9,-84.3\n" +
		"Blair,2 Main St,33.9,-84.3\n" +
		"Casey,,,\n" +
		"Dana,4 Main St,,\n"
	id := startImportPanelSession(t, handler, contents, importer.KindParticipant)

	mapping := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{
		"column_0": {"name"}, "column_1": {"address"}, "column_2": {"lat"}, "column_3": {"lng"},
	})
	recorder := httptest.NewRecorder()
	handler.HandleImportSession(recorder, mapping)
	assertPanelFragment(t, recorder)

	body := recorder.Body.String()
	for _, want := range []string{
		"import-row-error", "import-row-duplicate",
		"Duplicate row in this file", "Already in your roster",
		"33.9000, -84.3000", "Pending lookup",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("preview missing %q: %s", want, body)
		}
	}
	if got := strings.Count(body, `name="selected"`); got != 4 {
		t.Fatalf("selectable checkboxes = %d, want 4 (the error row is unselectable): %s", got, body)
	}
	if !strings.Contains(body, "2 of 5 rows selected") {
		t.Fatalf("duplicates should default to unselected: %s", body)
	}
}

func TestImportPanelDriverMappingOffersCapacity(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "drivers.csv", "name,address,passenger capacity\nAlex,1 Main St,4\n", importer.KindDriver, "")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)
	assertPanelFragment(t, recorder)

	if body := recorder.Body.String(); !strings.Contains(body, "Passenger capacity (excluding driver)") {
		t.Fatalf("driver mapping should offer capacity: %s", body)
	}
}

func TestImportMappingFromFormParticipantIgnoresCapacityValue(t *testing.T) {
	request := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/session/mapping?view=panel", url.Values{
		"column_0": {"name"}, "column_1": {"address"}, "column_2": {"capacity"},
	})
	if err := request.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	mapping, problems := importMappingFromForm(request, importer.Snapshot{
		Kind: importer.KindParticipant,
		Grid: importer.Grid{Headers: []string{"name", "address", "capacity"}},
	})

	if len(problems) != 0 {
		t.Fatalf("problems = %#v, want none", problems)
	}
	if mapping.CapacityColumn != importer.UnmappedColumn {
		t.Fatalf("CapacityColumn = %d, want unmapped", mapping.CapacityColumn)
	}
	if len(mapping.Ignored) != 1 || mapping.Ignored[0] != 2 {
		t.Fatalf("Ignored = %#v, want [2]", mapping.Ignored)
	}
}

func TestImportPanelMappingProblemsRenderInline(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	id := startImportPanelSession(t, handler, "name,address\nAlex,1 Main St\n", importer.KindParticipant)

	request := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{
		"column_0": {"name"}, "column_1": {"name"},
	})
	recorder := httptest.NewRecorder()
	handler.HandleImportSession(recorder, request)
	assertPanelFragment(t, recorder)

	body := recorder.Body.String()
	if !strings.Contains(body, "Name is mapped to more than one column — pick one.") {
		t.Fatalf("expected duplicate-field problem: %s", body)
	}
	if !strings.Contains(body, "Choose a column for Address.") {
		t.Fatalf("expected missing-address problem: %s", body)
	}
	if !strings.Contains(body, `name="column_0"`) {
		t.Fatalf("mapping table should be re-rendered: %s", body)
	}
	if !strings.Contains(body, `<option value="address" selected>Address</option>`) {
		t.Fatalf("mapping problems should re-render the original snapshot selections: %s", body)
	}
}

func TestImportPanelAmbiguousMappingWarns(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "participants.csv", "name,full name,address\nAlex,Alex Ruiz,1 Main St\n", importer.KindParticipant, "")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)
	assertPanelFragment(t, recorder)

	if body := recorder.Body.String(); !strings.Contains(body, "Multiple columns look like Name — pick one.") {
		t.Fatalf("expected ambiguity warning: %s", body)
	}
}

func TestImportPreviewRendersFileWarnings(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	const warning = "file-level formula warning"
	recorder := httptest.NewRecorder()

	handler.renderTemplate(recorder, "import_preview", newImportPreviewView(importer.Snapshot{
		Grid:   importer.Grid{Warnings: []string{warning}},
		Status: importer.StatusPreviewing,
	}))

	if body := recorder.Body.String(); !strings.Contains(body, warning) || !strings.Contains(body, "alert-warning") {
		t.Fatalf("preview did not render file warning: %s", body)
	}
}

func TestImportPanelWorksheetPicker(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "roster.xlsx", twoSheetWorkbook(t), importer.KindParticipant, "")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)
	assertPanelFragment(t, recorder)

	body := recorder.Body.String()
	for _, want := range []string{"Choose a worksheet", `value="Sheet1"`, `value="Second"`, "Use this sheet", `form="import-upload-form"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("sheet fragment missing %q: %s", want, body)
		}
	}
}

func TestImportPanelUploadErrorRendersMessage(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "roster.txt", "name,address\n", importer.KindParticipant, "")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)
	assertPanelFragment(t, recorder)

	if body := recorder.Body.String(); !strings.Contains(body, "Import file must have a .csv or .xlsx extension") {
		t.Fatalf("expected the server message in the panel: %s", body)
	}
	var trigger triggerHeader
	header := recorder.Header().Get("HX-Trigger")
	if header == "" {
		t.Fatal("expected an error toast trigger")
	}
	if err := json.Unmarshal([]byte(header), &trigger); err != nil {
		t.Fatalf("decode HX-Trigger: %v", err)
	}
	if trigger.ShowToast.Type != toastTypeError {
		t.Fatalf("toast type = %q, want %q", trigger.ShowToast.Type, toastTypeError)
	}
}

func TestImportPanelCommitConflictRendersServerMessage(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	id := startImportPanelSession(t, handler, "name,address,lat,lng\nAlex,1 Main St,40,-73\n", importer.KindParticipant)

	mapping := newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{
		"column_0": {"name"}, "column_1": {"address"}, "column_2": {"lat"}, "column_3": {"lng"},
	})
	handler.HandleImportSession(httptest.NewRecorder(), mapping)

	commit := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.HandleImportSession(recorder, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", url.Values{"selected": {"0"}}))
		return recorder
	}
	if body := commit().Body.String(); !strings.Contains(body, "1 imported") {
		t.Fatalf("first commit = %s", body)
	}

	retry := commit()
	assertPanelFragment(t, retry)
	if body := retry.Body.String(); !strings.Contains(body, importer.ErrCommitConsumed.Error()) {
		t.Fatalf("retry should show the conflict message: %s", body)
	}
}

func TestImportPanelCancelClearsPanelAndSession(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	id := startImportPanelSession(t, handler, "name,address\nAlex,1 Main St\n", importer.KindParticipant)

	recorder := httptest.NewRecorder()
	handler.HandleImportSession(recorder, newImportPanelRequest(http.MethodDelete, "/api/v1/imports/"+id+"?view=panel"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("cancel body = %q, want empty", body)
	}

	after := httptest.NewRecorder()
	handler.HandleImportSession(after, newImportRequest(http.MethodGet, "/api/v1/imports/"+id, nil))
	if after.Code != http.StatusNotFound {
		t.Fatalf("get-after-cancel status = %d, want %d", after.Code, http.StatusNotFound)
	}
}

func TestImportPanelRequiresViewParameter(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	assertJSONContentType(t, recorder)
}

func TestImportPanelSessionRoutesStayJSONWithoutViewParameter(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	id := startImportPanelSession(t, handler, "name,address\nAlex,1 Main St\n", importer.KindParticipant)

	request := newImportRequest(http.MethodGet, "/api/v1/imports/"+id, nil)
	request.Header.Set("HX-Request", "true")
	recorder := httptest.NewRecorder()
	handler.HandleImportSession(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	assertJSONContentType(t, recorder)
}

func TestImportPanelRejectsCrossOriginRequests(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
	upload.Host = "rides.example.org"
	upload.Header.Set("Origin", "https://attacker.example")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	assertJSONContentType(t, recorder)
}

func TestImportPanelAcceptsTunnelledSameOriginRequests(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	upload := newImportPanelUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
	upload.Host = "rides.example.org"
	upload.Header.Set("Origin", "https://rides.example.org")
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, upload)

	assertPanelFragment(t, recorder)
	importPanelSessionID(t, recorder.Body.String())
}

func startImportPanelSession(t *testing.T, handler *Handler, contents string, kind importer.Kind) string {
	t.Helper()
	filename := "participants.csv"
	if kind == importer.KindDriver {
		filename = "drivers.csv"
	}
	recorder := httptest.NewRecorder()
	handler.HandleCreateImport(recorder, newImportPanelUploadRequest(t, filename, contents, kind, ""))
	assertPanelFragment(t, recorder)
	return importPanelSessionID(t, recorder.Body.String())
}

// importPanelSessionID reads the session ID out of the rendered fragment's
// htmx URLs, which is the only place the panel carries it.
func importPanelSessionID(t *testing.T, fragment string) string {
	t.Helper()
	const prefix = "/api/v1/imports/"
	_, id, ok := strings.Cut(fragment, prefix)
	if !ok {
		t.Fatalf("fragment has no session URL: %s", fragment)
	}
	if cut := strings.IndexAny(id, `/?"`); cut >= 0 {
		id = id[:cut]
	}
	if !validImportSessionID(id) {
		t.Fatalf("fragment session ID = %q", id)
	}
	return id
}

func newImportPanelUploadRequest(t *testing.T, filename, contents string, kind importer.Kind, sheet string) *http.Request {
	t.Helper()
	request := newImportUploadRequest(t, filename, contents, kind, sheet)
	request.URL.RawQuery = "view=" + importPanelViewValue
	return request
}

func newImportPanelRequest(method, path string) *http.Request {
	request := newImportRequest(method, path, nil)
	request.Header.Set("HX-Request", "true")
	return request
}

func newImportPanelFormRequest(method, path string, values url.Values) *http.Request {
	request := newImportRequest(method, path, bytes.NewReader([]byte(values.Encode())))
	request.Header.Set("HX-Request", "true")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func assertPanelFragment(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

func TestRosterPagesRenderImportPanel(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		kind    importer.Kind
		render  func(*Handler, http.ResponseWriter, *http.Request)
		listID  string
		heading string
	}{
		{"participants", "/participants", importer.KindParticipant, (*Handler).HandleParticipantsPage, "participants-list", "Add Participant"},
		{"drivers", "/drivers", importer.KindDriver, (*Handler).HandleDriversPage, "drivers-list", "Add Driver"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := newTestPageHandler(t)
			recorder := httptest.NewRecorder()
			tt.render(handler, recorder, httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			body := recorder.Body.String()
			for _, want := range []string{
				"Import from spreadsheet",
				`hx-post="/api/v1/imports?view=panel"`,
				`hx-encoding="multipart/form-data"`,
				`accept=".csv,.xlsx"`,
				`steps.querySelector('.import-cancel')`,
				`cancel.click()`,
				`steps.innerHTML = ''`,
				`value="` + string(tt.kind) + `"`,
				`id="import-steps"`,
				tt.heading,
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("%s page missing %q", tt.name, want)
				}
			}
			if strings.Index(body, `cancel.click()`) > strings.Index(body, `steps.innerHTML = ''`) {
				t.Fatalf("%s page must cancel the prior session before clearing import steps", tt.name)
			}
			if strings.Index(body, `id="import-file"`) > strings.Index(body, `id="import-steps"`) {
				t.Fatalf("%s page must keep the file input outside the swapped step container", tt.name)
			}
			if !strings.Contains(body, `id="`+tt.listID+`"`) {
				t.Fatalf("%s page missing roster list container", tt.name)
			}
		})
	}
}
