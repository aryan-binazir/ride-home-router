package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"ride-home-router/internal/sqlite"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestImportHTTPHappyPath(t *testing.T) {
	geocoder := &importTestGeocoder{}
	handler, db := newImportTestHandler(t, geocoder)

	upload := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\nBlair,2 Main St\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d body=%q", uploadRecorder.Code, http.StatusCreated, uploadRecorder.Body.String())
	}
	created := decodeImportSnapshot(t, uploadRecorder)
	if created.Status != importer.StatusMapping || len(created.Headers) != 2 || created.Mapping.NameColumn != 0 || created.Mapping.AddressColumn != 1 {
		t.Fatalf("created snapshot = %#v", created)
	}
	if len(created.Rows) != 0 {
		t.Fatalf("created rows = %d, want 0 before validation", len(created.Rows))
	}

	mapping := newImportJSONRequest(t, http.MethodPut, "/api/v1/imports/"+created.ID+"/mapping", map[string]int{
		"name_column": 0, "address_column": 1,
	})
	mappingRecorder := httptest.NewRecorder()
	handler.HandleImportSession(mappingRecorder, mapping)
	if mappingRecorder.Code != http.StatusOK {
		t.Fatalf("mapping status = %d, want %d body=%q", mappingRecorder.Code, http.StatusOK, mappingRecorder.Body.String())
	}
	preview := decodeImportSnapshot(t, mappingRecorder)
	if preview.Status != importer.StatusPreviewing || len(preview.Rows) != 2 {
		t.Fatalf("preview snapshot = %#v", preview)
	}

	finished := waitForImportHTTPGeocoding(t, handler, created.ID)
	if finished.GeocodeProgress != (importGeocodeProgressJSON{Done: 2, Total: 2}) {
		t.Fatalf("geocode progress = %#v", finished.GeocodeProgress)
	}
	for i, row := range finished.Rows {
		if !row.HasCoordinates || row.NeedsGeocoding || !row.Selected || len(row.Errors) != 0 {
			t.Errorf("row %d = %#v", i, row)
		}
	}
	if geocoder.callCount() != 2 {
		t.Fatalf("geocoder calls = %d, want 2", geocoder.callCount())
	}

	selection := newImportJSONRequest(t, http.MethodPut, "/api/v1/imports/"+created.ID+"/selection", []bool{true, true})
	selectionRecorder := httptest.NewRecorder()
	handler.HandleImportSession(selectionRecorder, selection)
	if selectionRecorder.Code != http.StatusOK {
		t.Fatalf("selection status = %d, want %d body=%q", selectionRecorder.Code, http.StatusOK, selectionRecorder.Body.String())
	}
	selected := decodeImportSnapshot(t, selectionRecorder)
	if !selected.Rows[0].Selected || !selected.Rows[1].Selected {
		t.Fatalf("selected rows = %#v", selected.Rows)
	}

	commit := newImportRequest(http.MethodPost, "/api/v1/imports/"+created.ID+"/commit", nil)
	commitRecorder := httptest.NewRecorder()
	handler.HandleImportSession(commitRecorder, commit)
	if commitRecorder.Code != http.StatusOK {
		t.Fatalf("commit status = %d, want %d body=%q", commitRecorder.Code, http.StatusOK, commitRecorder.Body.String())
	}
	var result importCommitResultJSON
	decodeImportResponse(t, commitRecorder, &result)
	if result != (importCommitResultJSON{Created: 2}) {
		t.Fatalf("commit result = %#v", result)
	}

	participants, err := db.Participants().List(context.Background(), "")
	if err != nil {
		t.Fatalf("list committed participants: %v", err)
	}
	if len(participants) != 2 || participants[0].Name != "Alex" || participants[1].Name != "Blair" {
		t.Fatalf("committed participants = %#v", participants)
	}
}

func TestImportHTTPXLSXSheetPicker(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	contents := twoSheetWorkbook(t)
	upload := newImportUploadRequest(t, "roster.xlsx", contents, importer.KindParticipant, "")
	recorder := httptest.NewRecorder()

	handler.HandleCreateImport(recorder, upload)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusUnprocessableEntity, recorder.Body.String())
	}
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Sheets []string `json:"sheets"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeImportResponse(t, recorder, &response)
	if response.Error.Code != "WORKSHEET_REQUIRED" || len(response.Error.Details.Sheets) != 2 || response.Error.Details.Sheets[0] != "Sheet1" || response.Error.Details.Sheets[1] != "Second" {
		t.Fatalf("sheet picker response = %#v", response)
	}
}

func TestImportHTTPSecurity(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})

	tests := []struct {
		name    string
		request func(*testing.T) *http.Request
	}{
		{
			name: "hostile origin",
			request: func(t *testing.T) *http.Request {
				r := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
				r.Header.Set("Origin", "https://evil.example")
				return r
			},
		},
		{
			name: "bad host",
			request: func(t *testing.T) *http.Request {
				r := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
				r.Host = "evil.example"
				return r
			},
		},
		{
			name: "missing HX-Request",
			request: func(t *testing.T) *http.Request {
				r := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
				r.Header.Del("HX-Request")
				return r
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.HandleCreateImport(recorder, tt.request(t))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusForbidden, recorder.Body.String())
			}
			assertJSONContentType(t, recorder)
		})
	}
}

func TestImportHTTPUploadTooLarge(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	upload := newImportUploadRequest(t, "large.csv", strings.Repeat("x", int(MaxImportUploadBytes)), importer.KindParticipant, "")
	recorder := httptest.NewRecorder()

	handler.HandleCreateImport(recorder, upload)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), fmt.Sprintf("%d", MaxImportUploadBytes)) {
		t.Fatalf("response does not include byte limit: %q", recorder.Body.String())
	}
}

func TestImportHTTPWrongID(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	request := newImportRequest(http.MethodGet, "/api/v1/imports/00000000000000000000000000000000", nil)
	recorder := httptest.NewRecorder()

	handler.HandleImportSession(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestImportHTTPDoubleCommit(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	id := createCoordinateCompleteImport(t, handler)

	first := httptest.NewRecorder()
	handler.HandleImportSession(first, newImportRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first commit status = %d body=%q", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.HandleImportSession(second, newImportRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second commit status = %d, want %d body=%q", second.Code, http.StatusConflict, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "COMMIT_CONSUMED") {
		t.Fatalf("second commit response = %q", second.Body.String())
	}
}

func TestImportHTTPCommitDuringGeocoding(t *testing.T) {
	geocoder := &blockingImportGeocoder{started: make(chan struct{})}
	handler, _ := newImportTestHandler(t, geocoder)
	upload := newImportUploadRequest(t, "participants.csv", "name,address\nAlex,1 Main St\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	created := decodeImportSnapshot(t, uploadRecorder)

	mapping := newImportJSONRequest(t, http.MethodPut, "/api/v1/imports/"+created.ID+"/mapping", map[string]int{"name_column": 0, "address_column": 1})
	mappingRecorder := httptest.NewRecorder()
	handler.HandleImportSession(mappingRecorder, mapping)
	if mappingRecorder.Code != http.StatusOK {
		t.Fatalf("mapping status = %d body=%q", mappingRecorder.Code, mappingRecorder.Body.String())
	}
	select {
	case <-geocoder.started:
	case <-time.After(time.Second):
		t.Fatal("geocoding did not start")
	}

	commit := newImportRequest(http.MethodPost, "/api/v1/imports/"+created.ID+"/commit", nil)
	commitRecorder := httptest.NewRecorder()
	handler.HandleImportSession(commitRecorder, commit)
	if commitRecorder.Code != http.StatusConflict {
		t.Fatalf("commit status = %d, want %d body=%q", commitRecorder.Code, http.StatusConflict, commitRecorder.Body.String())
	}
	if !strings.Contains(commitRecorder.Body.String(), "GEOCODING_IN_PROGRESS") {
		t.Fatalf("commit response = %q", commitRecorder.Body.String())
	}
}

func TestImportHTTPOversizeRowCount(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	var csv strings.Builder
	csv.WriteString("name,address,lat,lng\n")
	for i := 0; i <= importer.MaxDataRows; i++ {
		fmt.Fprintf(&csv, "Rider %d,%d Main St,40,-73\n", i, i)
	}
	upload := newImportUploadRequest(t, "too-many.csv", csv.String(), importer.KindParticipant, "")
	recorder := httptest.NewRecorder()

	handler.HandleCreateImport(recorder, upload)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d body=%q", recorder.Code, http.StatusUnprocessableEntity, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), fmt.Sprintf("%d data rows", importer.MaxDataRows)) {
		t.Fatalf("response does not include row limit: %q", recorder.Body.String())
	}
}

func TestImportHTTPCancel(t *testing.T) {
	handler, _ := newImportTestHandler(t, &importTestGeocoder{})
	upload := newImportUploadRequest(t, "participants.csv", "name,address,lat,lng\nAlex,1 Main St,40,-73\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	id := decodeImportSnapshot(t, uploadRecorder).ID

	cancelRecorder := httptest.NewRecorder()
	handler.HandleImportSession(cancelRecorder, newImportRequest(http.MethodDelete, "/api/v1/imports/"+id, nil))
	if cancelRecorder.Code != http.StatusNoContent || cancelRecorder.Body.Len() != 0 {
		t.Fatalf("cancel response = status %d body=%q", cancelRecorder.Code, cancelRecorder.Body.String())
	}

	getRecorder := httptest.NewRecorder()
	handler.HandleImportSession(getRecorder, newImportRequest(http.MethodGet, "/api/v1/imports/"+id, nil))
	if getRecorder.Code != http.StatusNotFound {
		t.Fatalf("get-after-cancel status = %d, want %d", getRecorder.Code, http.StatusNotFound)
	}
}

func TestWriteImportStoreErrorStatuses(t *testing.T) {
	handler := &Handler{}
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"not found", importer.ErrSessionNotFound, http.StatusNotFound},
		{"commit consumed", importer.ErrCommitConsumed, http.StatusConflict},
		{"invalid state", importer.ErrInvalidSessionState, http.StatusConflict},
		{"geocoding", importer.ErrGeocodingInProgress, http.StatusConflict},
		{"selection", importer.ErrInvalidSelection, http.StatusUnprocessableEntity},
		{"geocode limit", importer.ErrTooManyGeocodeAddresses, http.StatusUnprocessableEntity},
		{"store full", importer.ErrStoreFull, http.StatusTooManyRequests},
		{"store closed", importer.ErrStoreClosed, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := newImportRequest(http.MethodGet, "/api/v1/imports/"+strings.Repeat("a", 32), nil)
			if got := handler.writeImportStoreError(recorder, request, "", fmt.Errorf("context: %w", tt.err)); got != tt.status {
				t.Fatalf("returned status = %d, want %d", got, tt.status)
			}
			if recorder.Code != tt.status {
				t.Fatalf("response status = %d, want %d", recorder.Code, tt.status)
			}
			assertJSONContentType(t, recorder)
		})
	}
}

type importTestGeocoder struct {
	geocoding.Geocoder
	mu    sync.Mutex
	calls int
}

func (g *importTestGeocoder) GeocodeWithRetry(_ context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
	g.mu.Lock()
	g.calls++
	call := g.calls
	g.mu.Unlock()
	return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40 + float64(call)/100, Lng: -73 - float64(call)/100}}, nil
}

func (g *importTestGeocoder) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

type blockingImportGeocoder struct {
	geocoding.Geocoder
	started chan struct{}
	once    sync.Once
}

func (g *blockingImportGeocoder) GeocodeWithRetry(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
	g.once.Do(func() { close(g.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func newImportTestHandler(t *testing.T, geocoder geocoding.Geocoder) (*Handler, *sqlite.Store) {
	t.Helper()
	db, err := sqlite.New(filepath.Join(t.TempDir(), "imports-test.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	importStore := importer.NewStore(geocoder, db)
	handler := &Handler{DB: db, Geocoder: geocoder, ImportSession: importStore, Renderer: loadEmbeddedTemplates(t)}
	t.Cleanup(func() {
		importStore.Close()
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})
	return handler, db
}

func newImportUploadRequest(t *testing.T, filename, contents string, kind importer.Kind, sheet string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("kind", string(kind)); err != nil {
		t.Fatalf("write kind field: %v", err)
	}
	if sheet != "" {
		if err := writer.WriteField("sheet", sheet); err != nil {
			t.Fatalf("write sheet field: %v", err)
		}
	}
	file, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := file.Write([]byte(contents)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/imports", &body)
	request.Host = "localhost:8080"
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("HX-Request", "true")
	return request
}

func newImportJSONRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	request := newImportRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func newImportRequest(method, path string, body *bytes.Reader) *http.Request {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = body
	}
	request := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	request.Host = "127.0.0.1:8080"
	return request
}

func decodeImportSnapshot(t *testing.T, recorder *httptest.ResponseRecorder) importSnapshotJSON {
	t.Helper()
	var snapshot importSnapshotJSON
	decodeImportResponse(t, recorder, &snapshot)
	return snapshot
}

func decodeImportResponse(t *testing.T, recorder *httptest.ResponseRecorder, destination any) {
	t.Helper()
	assertJSONContentType(t, recorder)
	if err := json.NewDecoder(recorder.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v body=%q", err, recorder.Body.String())
	}
}

func assertJSONContentType(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func waitForImportHTTPGeocoding(t *testing.T, handler *Handler, id string) importSnapshotJSON {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		handler.HandleImportSession(recorder, newImportRequest(http.MethodGet, "/api/v1/imports/"+id, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("snapshot status = %d body=%q", recorder.Code, recorder.Body.String())
		}
		snapshot := decodeImportSnapshot(t, recorder)
		if !snapshot.GeocodeProgress.Running {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("geocoding did not finish")
	return importSnapshotJSON{}
}

func createCoordinateCompleteImport(t *testing.T, handler *Handler) string {
	t.Helper()
	upload := newImportUploadRequest(t, "participants.csv", "name,address,lat,lng\nAlex,1 Main St,40,-73\n", importer.KindParticipant, "")
	uploadRecorder := httptest.NewRecorder()
	handler.HandleCreateImport(uploadRecorder, upload)
	if uploadRecorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body=%q", uploadRecorder.Code, uploadRecorder.Body.String())
	}
	created := decodeImportSnapshot(t, uploadRecorder)
	mapping := newImportJSONRequest(t, http.MethodPut, "/api/v1/imports/"+created.ID+"/mapping", map[string]int{
		"name_column": 0, "address_column": 1, "latitude_column": 2, "longitude_column": 3,
	})
	mappingRecorder := httptest.NewRecorder()
	handler.HandleImportSession(mappingRecorder, mapping)
	if mappingRecorder.Code != http.StatusOK {
		t.Fatalf("mapping status = %d body=%q", mappingRecorder.Code, mappingRecorder.Body.String())
	}
	return created.ID
}

func twoSheetWorkbook(t *testing.T) string {
	t.Helper()
	workbook := excelize.NewFile()
	if err := workbook.SetSheetRow("Sheet1", "A1", &[]any{"name", "address"}); err != nil {
		t.Fatalf("write first sheet headers: %v", err)
	}
	if err := workbook.SetSheetRow("Sheet1", "A2", &[]any{"Alex", "1 Main St"}); err != nil {
		t.Fatalf("write first sheet row: %v", err)
	}
	if _, err := workbook.NewSheet("Second"); err != nil {
		t.Fatalf("create second sheet: %v", err)
	}
	if err := workbook.SetSheetRow("Second", "A1", &[]any{"name", "address"}); err != nil {
		t.Fatalf("write second sheet headers: %v", err)
	}
	if err := workbook.SetSheetRow("Second", "A2", &[]any{"Blair", "2 Main St"}); err != nil {
		t.Fatalf("write second sheet row: %v", err)
	}
	var buffer bytes.Buffer
	if err := workbook.Write(&buffer); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	if err := workbook.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return buffer.String()
}
