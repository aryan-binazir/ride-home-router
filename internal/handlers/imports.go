package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/importer"
	"strings"
	"time"
)

const (
	// MaxImportUploadBytes is the maximum size of a complete multipart import request.
	MaxImportUploadBytes int64 = 10 << 20
	// MaxImportJSONBytes is the maximum size of an import JSON request body.
	MaxImportJSONBytes int64 = 1 << 20
)

const importMultipartMemory = 1 << 20

type importMappingJSON struct {
	NameColumn        int                      `json:"name_column"`
	AddressColumn     int                      `json:"address_column"`
	AddressNameColumn int                      `json:"address_name_column"`
	CapacityColumn    int                      `json:"capacity_column"`
	Ambiguous         map[importer.Field][]int `json:"ambiguous"`
	Ignored           []int                    `json:"ignored"`
}

type importMappingRequest struct {
	NameColumn        *int `json:"name_column"`
	AddressColumn     *int `json:"address_column"`
	AddressNameColumn *int `json:"address_name_column"`
	CapacityColumn    *int `json:"capacity_column"`
}

type importGeocodeProgressJSON struct {
	Done    int  `json:"done"`
	Total   int  `json:"total"`
	Running bool `json:"running"`
}

type importRowJSON struct {
	SourceRow int `json:"source_row"`

	Name        string  `json:"name"`
	Address     string  `json:"address"`
	AddressName string  `json:"address_name"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Capacity    int     `json:"capacity"`

	HasCoordinates      bool `json:"has_coordinates"`
	NeedsGeocoding      bool `json:"needs_geocoding"`
	DuplicateInFile     bool `json:"duplicate_in_file"`
	DuplicateOfExisting bool `json:"duplicate_of_existing"`
	Selected            bool `json:"selected"`

	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type importCommitResultJSON struct {
	Created     int `json:"created"`
	Updated     int `json:"updated"`
	NotSelected int `json:"not_selected"`
}

type importSnapshotJSON struct {
	ID              string                    `json:"id"`
	Kind            importer.Kind             `json:"kind"`
	Filename        string                    `json:"filename"`
	Status          importer.Status           `json:"status"`
	Headers         []string                  `json:"headers"`
	Warnings        []string                  `json:"warnings"`
	Mapping         importMappingJSON         `json:"mapping"`
	GeocodeProgress importGeocodeProgressJSON `json:"geocode_progress"`
	Rows            []importRowJSON           `json:"rows"`
	Failure         string                    `json:"failure,omitempty"`
	CommitResult    importCommitResultJSON    `json:"commit_result"`
}

// HandleCreateImport handles POST /api/v1/imports.
func (h *Handler) HandleCreateImport(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := http.StatusInternalServerError
	rowCount := -1
	defer func() { logImportRequest(r.Method, "upload", "", status, rowCount, started) }()

	if !validImportRequestSource(r) {
		status = http.StatusForbidden
		h.writeError(w, status, "FORBIDDEN", "Import requests must come from this application's own origin", nil)
		return
	}
	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		h.writeError(w, status, "METHOD_NOT_ALLOWED", "Method not allowed", nil)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		status = http.StatusForbidden
		h.writeError(w, status, "FORBIDDEN", "Import uploads require HX-Request: true", nil)
		return
	}
	if h.ImportSession == nil {
		status = http.StatusServiceUnavailable
		h.writeError(w, status, "SERVICE_UNAVAILABLE", "Import sessions are unavailable", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxImportUploadBytes)
	parseErr := r.ParseMultipartForm(importMultipartMemory)
	if r.MultipartForm != nil {
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				log.Printf("[ERROR] Failed to remove import multipart files: err=%v", err)
			}
		}()
	}
	if parseErr != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](parseErr); ok {
			status = h.writeImportError(w, r, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("Import uploads are limited to %d bytes", MaxImportUploadBytes), nil)
			return
		}
		status = h.writeImportError(w, r, "", http.StatusBadRequest, "INVALID_MULTIPART_FORM", "Invalid multipart import request", nil)
		return
	}

	kind := importer.Kind(r.FormValue("kind"))
	if kind != importer.KindParticipant && kind != importer.KindDriver {
		status = h.writeImportError(w, r, "", http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Import kind must be participant or driver", nil)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		status = h.writeImportError(w, r, "", http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Import file is required", nil)
		return
	}
	defer func() { _ = file.Close() }()

	format, ok := importFormat(header.Filename)
	if !ok {
		status = h.writeImportError(w, r, "", http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Import file must have a .csv or .xlsx extension", nil)
		return
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		status = h.writeImportError(w, r, "", http.StatusBadRequest, "INVALID_IMPORT_FILE", "Import file could not be read", nil)
		return
	}

	sheet := strings.TrimSpace(r.FormValue("sheet"))
	if format == importer.FormatXLSX && sheet == "" {
		sheets, sheetsErr := importer.Sheets(bytes.NewReader(contents))
		if sheetsErr != nil {
			status = h.writeImportError(w, r, "", http.StatusUnprocessableEntity, "VALIDATION_ERROR", sheetsErr.Error(), nil)
			return
		}
		if len(sheets) > 1 {
			if h.wantsImportPanel(r) {
				h.renderTemplate(w, "import_sheets", importSheetsView{Sheets: sheets})
				status = http.StatusOK
				return
			}
			status = http.StatusUnprocessableEntity
			h.writeError(w, status, "WORKSHEET_REQUIRED", "XLSX file has multiple non-empty worksheets; choose a worksheet explicitly", map[string]any{"sheets": sheets})
			return
		}
	}

	grid, err := importer.Parse(bytes.NewReader(contents), format, sheet)
	if err != nil {
		status = h.writeImportError(w, r, "", http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
		return
	}
	rowCount = grid.Len()
	snapshot, err := h.ImportSession.Create(kind, header.Filename, grid)
	if err != nil {
		status = h.writeImportStoreError(w, r, "", err)
		return
	}

	if h.wantsImportPanel(r) {
		h.renderTemplate(w, "import_mapping", newImportMappingView(snapshot, nil))
		status = http.StatusOK
		return
	}
	status = http.StatusCreated
	h.writeJSON(w, status, newImportSnapshotJSON(snapshot))
}

// HandleImportSession handles all /api/v1/imports/{id} routes.
func (h *Handler) HandleImportSession(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	status := http.StatusInternalServerError
	rowCount := -1
	id, action, pathOK := parseImportSessionPath(r.URL.Path)
	sanitizeImportRequestPath(r, id, action, pathOK)
	defer func() { logImportRequest(r.Method, action, id, status, rowCount, started) }()

	if !validImportRequestSource(r) {
		status = http.StatusForbidden
		h.writeError(w, status, "FORBIDDEN", "Import requests must come from this application's own origin", nil)
		return
	}
	if h.ImportSession == nil {
		status = http.StatusServiceUnavailable
		h.writeError(w, status, "SERVICE_UNAVAILABLE", "Import sessions are unavailable", nil)
		return
	}
	if !pathOK {
		status = http.StatusNotFound
		h.writeError(w, status, "NOT_FOUND", "Import session not found", nil)
		return
	}

	panel := h.wantsImportPanel(r)
	if !panel && (action == "mapping" || action == "selection" || action == "commit") {
		r.Body = http.MaxBytesReader(w, r.Body, MaxImportJSONBytes)
	}
	switch action {
	case "":
		switch {
		case r.Method == http.MethodGet && panel:
			status, rowCount = h.renderImportPanelSnapshot(w, r, id)
		case r.Method == http.MethodGet:
			status, rowCount = h.getImportSession(w, id)
		case r.Method == http.MethodDelete && panel:
			status = h.cancelImportPanel(w, id)
		case r.Method == http.MethodDelete:
			status = h.cancelImportSession(w, id)
		default:
			status = writeImportMethodNotAllowed(h, w)
		}
	case "mapping":
		if r.Method != http.MethodPut {
			status = writeImportMethodNotAllowed(h, w)
			return
		}
		if panel {
			status, rowCount = h.applyImportPanelMapping(w, r, id)
			return
		}
		status, rowCount = h.updateImportMapping(w, r, id)
	case "selection":
		if r.Method != http.MethodPut {
			status = writeImportMethodNotAllowed(h, w)
			return
		}
		if panel {
			status, rowCount = h.applyImportPanelSelection(w, r, id)
			return
		}
		status, rowCount = h.updateImportSelection(w, r, id)
	case "commit":
		if r.Method != http.MethodPost {
			status = writeImportMethodNotAllowed(h, w)
			return
		}
		if panel {
			status = h.commitImportPanel(w, r, id)
			return
		}
		status = h.commitImportSession(w, r, id)
	default:
		status = http.StatusNotFound
		h.writeError(w, status, "NOT_FOUND", "Import session route not found", nil)
	}
}

func (h *Handler) getImportSession(w http.ResponseWriter, id string) (int, int) {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
		return http.StatusNotFound, -1
	}
	h.writeJSON(w, http.StatusOK, newImportSnapshotJSON(snapshot))
	return http.StatusOK, len(snapshot.Rows)
}

func (h *Handler) updateImportMapping(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
		return http.StatusNotFound, -1
	}
	var request importMappingRequest
	if err := decodeImportJSON(r, &request); err != nil {
		return h.writeImportJSONBodyError(w, r, id, err), -1
	}
	mapping := mergeImportMapping(snapshot, request)
	updated, err := h.ImportSession.ApplyMapping(id, mapping)
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), len(updated.Rows)
	}
	h.writeJSON(w, http.StatusOK, newImportSnapshotJSON(updated))
	return http.StatusOK, len(updated.Rows)
}

func (h *Handler) updateImportSelection(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	var selected []bool
	if err := decodeImportJSON(r, &selected); err != nil {
		return h.writeImportJSONBodyError(w, r, id, err), -1
	}
	snapshot, err := h.ImportSession.SelectRows(id, selected)
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), -1
	}
	h.writeJSON(w, http.StatusOK, newImportSnapshotJSON(snapshot))
	return http.StatusOK, len(snapshot.Rows)
}

func (h *Handler) commitImportSession(w http.ResponseWriter, r *http.Request, id string) int {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		return h.writeImportJSONBodyError(w, r, id, err)
	}
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
		return http.StatusNotFound
	}
	result, err := h.ImportSession.Commit(r.Context(), id, snapshot.Selected)
	if err != nil {
		return h.writeImportStoreError(w, r, id, err)
	}
	h.writeJSON(w, http.StatusOK, newImportCommitResultJSON(result))
	return http.StatusOK
}

func (h *Handler) writeImportJSONBodyError(w http.ResponseWriter, r *http.Request, id string, err error) int {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return h.writeImportError(w, r, id, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("Import JSON bodies are limited to %d bytes", MaxImportJSONBytes), nil)
	}
	h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST_BODY", "Invalid request body", nil)
	return http.StatusBadRequest
}

func (h *Handler) cancelImportSession(w http.ResponseWriter, id string) int {
	if !h.ImportSession.Cancel(id) {
		h.writeError(w, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
		return http.StatusNotFound
	}
	w.WriteHeader(http.StatusNoContent)
	return http.StatusNoContent
}

func (h *Handler) writeImportStoreError(w http.ResponseWriter, r *http.Request, sessionID string, err error) int {
	switch {
	case errors.Is(err, importer.ErrSessionNotFound):
		return h.writeImportError(w, r, sessionID, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
	case errors.Is(err, importer.ErrCommitConsumed):
		return h.writeImportError(w, r, sessionID, http.StatusConflict, "COMMIT_CONSUMED", err.Error(), nil)
	case errors.Is(err, importer.ErrInvalidSessionState):
		return h.writeImportError(w, r, sessionID, http.StatusConflict, "INVALID_SESSION_STATE", err.Error(), nil)
	case errors.Is(err, importer.ErrGeocodingInProgress):
		return h.writeImportError(w, r, sessionID, http.StatusConflict, "GEOCODING_IN_PROGRESS", err.Error(), nil)
	case errors.Is(err, importer.ErrInvalidSelection):
		return h.writeImportError(w, r, sessionID, http.StatusUnprocessableEntity, "INVALID_SELECTION", err.Error(), nil)
	case errors.Is(err, importer.ErrTooManyGeocodeAddresses):
		return h.writeImportError(w, r, sessionID, http.StatusUnprocessableEntity, "GEOCODE_LIMIT_EXCEEDED", fmt.Sprintf("Import needs more than the limit of %d unique addresses requiring geocoding", importer.MaxGeocodeAddresses), nil)
	case errors.Is(err, importer.ErrStoreFull):
		return h.writeImportError(w, r, sessionID, http.StatusTooManyRequests, "IMPORT_STORE_FULL", fmt.Sprintf("At most %d import sessions can be active", importer.MaxConcurrentSessions), nil)
	case errors.Is(err, importer.ErrStoreClosed):
		return h.writeImportError(w, r, sessionID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Import sessions are unavailable", nil)
	default:
		log.Printf("[ERROR] Internal error: %v", err)
		return h.writeImportError(w, r, sessionID, http.StatusInternalServerError, "INTERNAL_ERROR", messageGenericInternalError, nil)
	}
}

func newImportSnapshotJSON(snapshot importer.Snapshot) importSnapshotJSON {
	rows := make([]importRowJSON, len(snapshot.Rows))
	for i, row := range snapshot.Rows {
		selected := i < len(snapshot.Selected) && snapshot.Selected[i]
		rows[i] = importRowJSON{
			SourceRow: row.SourceRow, Name: row.Name, Address: row.Address, AddressName: row.AddressName,
			Latitude: row.Lat, Longitude: row.Lng, Capacity: row.Capacity,
			HasCoordinates: row.HasCoordinates, NeedsGeocoding: row.NeedsGeocoding,
			DuplicateInFile: row.DuplicateInFile, DuplicateOfExisting: row.DuplicateOfExisting, Selected: selected,
			Errors: append([]string{}, row.Errors...), Warnings: append([]string{}, row.Warnings...),
		}
	}
	return importSnapshotJSON{
		ID: snapshot.ID, Kind: snapshot.Kind, Filename: snapshot.Filename, Status: snapshot.Status,
		Headers: append([]string{}, snapshot.Grid.Headers...), Warnings: append([]string{}, snapshot.Grid.Warnings...),
		Mapping: newImportMappingJSON(snapshot.Mapping),
		GeocodeProgress: importGeocodeProgressJSON{
			Done: snapshot.GeocodeProgress.Done, Total: snapshot.GeocodeProgress.Total, Running: snapshot.GeocodeProgress.Running,
		},
		Rows: rows, Failure: snapshot.Failure, CommitResult: newImportCommitResultJSON(snapshot.CommitResult),
	}
}

func newImportMappingJSON(mapping importer.Mapping) importMappingJSON {
	return importMappingJSON{
		NameColumn: mapping.NameColumn, AddressColumn: mapping.AddressColumn, AddressNameColumn: mapping.AddressNameColumn,
		CapacityColumn: mapping.CapacityColumn,
		Ambiguous:      mapping.Ambiguous, Ignored: append([]int{}, mapping.Ignored...),
	}
}

func newImportCommitResultJSON(result importer.CommitResult) importCommitResultJSON {
	return importCommitResultJSON{Created: result.Created, Updated: result.Updated, NotSelected: result.NotSelected}
}

func mergeImportMapping(snapshot importer.Snapshot, request importMappingRequest) importer.Mapping {
	updates := []struct {
		field importer.Field
		value *int
	}{
		{importer.FieldName, request.NameColumn},
		{importer.FieldAddress, request.AddressColumn},
		{importer.FieldAddressName, request.AddressNameColumn},
		{importer.FieldCapacity, request.CapacityColumn},
	}
	assignments := make([]importer.FieldColumn, 0, len(updates))
	for _, update := range updates {
		if update.value != nil {
			assignments = append(assignments, importer.FieldColumn{Field: update.field, Column: *update.value})
		}
	}
	return snapshot.Mapping.Assign(assignments, len(snapshot.Grid.Headers)).Mapping
}

func decodeImportJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func importFormat(filename string) (importer.Format, bool) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		return importer.FormatCSV, true
	case ".xlsx":
		return importer.FormatXLSX, true
	default:
		return "", false
	}
}

func validImportRequestSource(r *http.Request) bool {
	return httpx.HasSameOrigin(r)
}

func parseImportSessionPath(path string) (id, action string, ok bool) {
	remainder, found := strings.CutPrefix(path, "/api/v1/imports/")
	if !found || remainder == "" {
		return "", "", false
	}
	parts := strings.Split(remainder, "/")
	if len(parts) == 1 && validImportSessionID(parts[0]) {
		return parts[0], "", true
	}
	if len(parts) == 2 && validImportSessionID(parts[0]) && parts[1] != "" {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func writeImportMethodNotAllowed(h *Handler, w http.ResponseWriter) int {
	h.writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Method not allowed", nil)
	return http.StatusMethodNotAllowed
}

func logImportRequest(method, action, id string, status, rows int, started time.Time) {
	method = safeImportMethod(method)
	switch action {
	case "":
		action = "session"
	case "mapping", "selection", "commit", "upload":
	default:
		action = "unknown"
	}
	if rows >= 0 {
		log.Printf("[HTTP] Import request: method=%s action=%s session=%s status=%d rows=%d duration=%s", method, action, shortImportSessionID(id), status, rows, time.Since(started).Round(time.Millisecond))
		return
	}
	log.Printf("[HTTP] Import request: method=%s action=%s session=%s status=%d duration=%s", method, action, shortImportSessionID(id), status, time.Since(started).Round(time.Millisecond))
}

func sanitizeImportRequestPath(r *http.Request, id, action string, pathOK bool) {
	if !pathOK {
		r.URL.Path = "/api/v1/imports/invalid"
		return
	}
	path := "/api/v1/imports/" + shortImportSessionID(id)
	switch action {
	case "":
	case "mapping", "selection", "commit":
		path += "/" + action
	default:
		path += "/unknown"
	}
	r.URL.Path = path
}

func safeImportMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete:
		return method
	default:
		return "UNKNOWN"
	}
}

func shortImportSessionID(id string) string {
	if !validImportSessionID(id) {
		return "-"
	}
	return id[:8]
}

func validImportSessionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, char := range id {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
