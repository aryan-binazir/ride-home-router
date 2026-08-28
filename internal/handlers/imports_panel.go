package handlers

import (
	"fmt"
	"log"
	"net/http"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/importer"
	"strconv"
)

const (
	importIgnoreValue             = "ignore"
	importPanelViewValue          = "panel"
	maxImportPanelFormBytes int64 = 1 << 20
)

// wantsImportPanel needs view=panel because JSON uploads also set HX-Request.
func (h *Handler) wantsImportPanel(r *http.Request) bool {
	return h.isHTMX(r) && r.URL.Query().Get("view") == importPanelViewValue
}

type importFieldOption struct {
	Value string
	Label string
}

type importColumnView struct {
	Index    int
	Header   string
	Selected string
	Warning  string
}

type importMappingView struct {
	SessionID string
	Filename  string
	IsDriver  bool
	Errors    []string
	Fields    []importFieldOption
	Columns   []importColumnView
}

type importRowView struct {
	Index       int
	SourceRow   int
	Name        string
	Address     string
	AddressName string
	Coordinates string
	Capacity    int
	State       string
	Notes       []string
	Selected    bool
	Selectable  bool
}

type importCommitBarView struct {
	SessionID string
	Selected  int
	Total     int
	Disabled  bool
}

type importPreviewView struct {
	SessionID    string
	Filename     string
	IsDriver     bool
	Warnings     []string
	Rows         []importRowView
	Geocoding    bool
	GeocodeDone  int
	GeocodeTotal int
	CommitBar    importCommitBarView
}

type importMessageView struct {
	SessionID string
	Message   string
}

type importSheetsView struct {
	Sheets []string
}

type importCommitView struct {
	Message         string
	IsDriver        bool
	ListElementID   string
	HasList         bool
	ParticipantList ParticipantListView
	DriverList      DriverListView
}

func importFieldOptions(isDriver bool) []importFieldOption {
	options := []importFieldOption{
		{Value: string(importer.FieldName), Label: "Name"},
		{Value: string(importer.FieldAddress), Label: "Address"},
		{Value: string(importer.FieldAddressName), Label: "Location name"},
		{Value: string(importer.FieldLatitude), Label: "Latitude"},
		{Value: string(importer.FieldLongitude), Label: "Longitude"},
	}
	if isDriver {
		options = append(options, importFieldOption{Value: string(importer.FieldCapacity), Label: "Passenger capacity (excluding driver)"})
	}
	return append(options, importFieldOption{Value: importIgnoreValue, Label: "Ignore"})
}

func importFieldLabel(field importer.Field) string {
	for _, option := range importFieldOptions(true) {
		if option.Value == string(field) {
			return option.Label
		}
	}
	return string(field)
}

func importFieldFromValue(value string, isDriver bool) (importer.Field, bool) {
	for _, option := range importFieldOptions(isDriver) {
		if option.Value != value || value == importIgnoreValue {
			continue
		}
		return importer.Field(option.Value), true
	}
	return "", false
}

func newImportMappingView(snapshot importer.Snapshot, validationErrors []string) importMappingView {
	isDriver := snapshot.Kind == importer.KindDriver
	selectedByColumn := make(map[int]string, len(snapshot.Grid.Headers))
	for _, binding := range importMappingColumns(snapshot.Mapping) {
		if binding.column == importer.UnmappedColumn {
			continue
		}
		if binding.field == importer.FieldCapacity && !isDriver {
			continue
		}
		selectedByColumn[binding.column] = string(binding.field)
	}

	warningByColumn := make(map[int]string)
	for field, columns := range snapshot.Mapping.Ambiguous {
		if field == importer.FieldCapacity && !isDriver {
			continue
		}
		for _, column := range columns {
			warningByColumn[column] = fmt.Sprintf("Multiple columns look like %s — pick one.", importFieldLabel(field))
		}
	}

	columns := make([]importColumnView, len(snapshot.Grid.Headers))
	for index, header := range snapshot.Grid.Headers {
		selected, ok := selectedByColumn[index]
		if !ok {
			selected = importIgnoreValue
		}
		columns[index] = importColumnView{Index: index, Header: header, Selected: selected, Warning: warningByColumn[index]}
	}

	return importMappingView{
		SessionID: snapshot.ID,
		Filename:  snapshot.Filename,
		IsDriver:  isDriver,
		Errors:    validationErrors,
		Fields:    importFieldOptions(isDriver),
		Columns:   columns,
	}
}

type importMappingBinding struct {
	field  importer.Field
	column int
}

func importMappingColumns(mapping importer.Mapping) []importMappingBinding {
	return []importMappingBinding{
		{importer.FieldName, mapping.NameColumn},
		{importer.FieldAddress, mapping.AddressColumn},
		{importer.FieldAddressName, mapping.AddressNameColumn},
		{importer.FieldLatitude, mapping.LatitudeColumn},
		{importer.FieldLongitude, mapping.LongitudeColumn},
		{importer.FieldCapacity, mapping.CapacityColumn},
	}
}

func setImportMappingColumn(mapping *importer.Mapping, field importer.Field, column int) {
	switch field {
	case importer.FieldName:
		mapping.NameColumn = column
	case importer.FieldAddress:
		mapping.AddressColumn = column
	case importer.FieldAddressName:
		mapping.AddressNameColumn = column
	case importer.FieldLatitude:
		mapping.LatitudeColumn = column
	case importer.FieldLongitude:
		mapping.LongitudeColumn = column
	case importer.FieldCapacity:
		mapping.CapacityColumn = column
	}
}

// importMappingFromForm treats every unlisted column as ignored.
func importMappingFromForm(r *http.Request, snapshot importer.Snapshot) (importer.Mapping, []string) {
	isDriver := snapshot.Kind == importer.KindDriver
	mapping := importer.Mapping{
		NameColumn:        importer.UnmappedColumn,
		AddressColumn:     importer.UnmappedColumn,
		AddressNameColumn: importer.UnmappedColumn,
		LatitudeColumn:    importer.UnmappedColumn,
		LongitudeColumn:   importer.UnmappedColumn,
		CapacityColumn:    importer.UnmappedColumn,
		Ambiguous:         make(map[importer.Field][]int),
	}

	claimed := make(map[importer.Field]bool)
	reported := make(map[importer.Field]bool)
	var problems []string
	for column := range snapshot.Grid.Headers {
		field, ok := importFieldFromValue(r.FormValue(fmt.Sprintf("column_%d", column)), isDriver)
		if !ok {
			mapping.Ignored = append(mapping.Ignored, column)
			continue
		}
		if claimed[field] {
			if !reported[field] {
				reported[field] = true
				problems = append(problems, fmt.Sprintf("%s is mapped to more than one column — pick one.", importFieldLabel(field)))
			}
			continue
		}
		claimed[field] = true
		setImportMappingColumn(&mapping, field, column)
	}

	if mapping.NameColumn == importer.UnmappedColumn {
		problems = append(problems, "Choose a column for Name.")
	}
	if mapping.AddressColumn == importer.UnmappedColumn {
		problems = append(problems, "Choose a column for Address.")
	}
	return mapping, problems
}

func importSelectionFromForm(r *http.Request, rowCount int) []bool {
	selected := make([]bool, rowCount)
	for _, value := range r.Form["selected"] {
		index, err := strconv.Atoi(value)
		if err != nil || index < 0 || index >= rowCount {
			continue
		}
		selected[index] = true
	}
	return selected
}

func newImportPreviewView(snapshot importer.Snapshot) importPreviewView {
	isDriver := snapshot.Kind == importer.KindDriver
	rows := make([]importRowView, len(snapshot.Rows))
	for index, row := range snapshot.Rows {
		rows[index] = importRowView{
			Index:       index,
			SourceRow:   row.SourceRow,
			Name:        row.Name,
			Address:     row.Address,
			AddressName: row.AddressName,
			Coordinates: importRowCoordinates(row),
			Capacity:    row.Capacity,
			State:       importRowState(row),
			Notes:       importRowNotes(row),
			Selected:    index < len(snapshot.Selected) && snapshot.Selected[index],
			Selectable:  len(row.Errors) == 0,
		}
	}

	// Geocoding can make a previously selected row unselectable.
	selectedCount := 0
	for _, row := range rows {
		if row.Selected && row.Selectable {
			selectedCount++
		}
	}
	geocoding := snapshot.GeocodeProgress.Running

	return importPreviewView{
		SessionID:    snapshot.ID,
		Filename:     snapshot.Filename,
		IsDriver:     isDriver,
		Warnings:     append([]string(nil), snapshot.Grid.Warnings...),
		Rows:         rows,
		Geocoding:    geocoding,
		GeocodeDone:  snapshot.GeocodeProgress.Done,
		GeocodeTotal: snapshot.GeocodeProgress.Total,
		CommitBar: importCommitBarView{
			SessionID: snapshot.ID,
			Selected:  selectedCount,
			Total:     len(snapshot.Rows),
			Disabled:  geocoding || selectedCount == 0 || snapshot.Status != importer.StatusPreviewing,
		},
	}
}

func importRowCoordinates(row importer.Row) string {
	if row.HasCoordinates {
		return fmt.Sprintf("%.4f, %.4f", row.Lat, row.Lng)
	}
	if row.NeedsGeocoding {
		return "Pending lookup"
	}
	return ""
}

func importRowState(row importer.Row) string {
	switch {
	case len(row.Errors) > 0:
		return "error"
	case row.DuplicateInFile || row.DuplicateOfExisting:
		return "duplicate"
	case len(row.Warnings) > 0:
		return "warning"
	default:
		return ""
	}
}

func importRowNotes(row importer.Row) []string {
	notes := make([]string, 0, len(row.Errors)+len(row.Warnings)+2)
	notes = append(notes, row.Errors...)
	if row.DuplicateOfExisting {
		notes = append(notes, "Already in your roster")
	}
	if row.DuplicateInFile {
		notes = append(notes, "Duplicate row in this file")
	}
	return append(notes, row.Warnings...)
}

func importCommitMessage(result importer.CommitResult) string {
	return fmt.Sprintf("%d imported, %d skipped as duplicates", result.Created, result.SkippedDuplicate)
}

// writeImportError returns the logical status even when HTMX needs HTTP 200 to swap.
func (h *Handler) writeImportError(w http.ResponseWriter, r *http.Request, sessionID string, status int, code, message string, details any) int {
	if h.wantsImportPanel(r) {
		h.setHTMXToast(w, message, toastTypeError)
		h.renderImportMessage(w, sessionID, message)
		return http.StatusOK
	}
	h.writeError(w, status, code, message, details)
	return status
}

func (h *Handler) renderImportStep(w http.ResponseWriter, snapshot importer.Snapshot) {
	switch snapshot.Status {
	case importer.StatusMapping:
		h.renderTemplate(w, "import_mapping", newImportMappingView(snapshot, nil))
	case importer.StatusPreviewing, importer.StatusCommitting:
		h.renderTemplate(w, "import_preview", newImportPreviewView(snapshot))
	case importer.StatusCommitted:
		h.renderTemplate(w, "import_result", importCommitView{Message: importCommitMessage(snapshot.CommitResult)})
	case importer.StatusFailed:
		h.renderImportMessage(w, snapshot.ID, importFailureMessage(snapshot))
	default:
		h.renderImportMessage(w, snapshot.ID, importFailureMessage(snapshot))
	}
}

func importFailureMessage(snapshot importer.Snapshot) string {
	if snapshot.Failure != "" {
		return snapshot.Failure
	}
	return messageGenericInternalError
}

func (h *Handler) renderImportMessage(w http.ResponseWriter, sessionID, message string) {
	h.renderTemplate(w, "import_message", importMessageView{SessionID: sessionID, Message: message})
}

func (h *Handler) renderImportPanelSnapshot(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil), -1
	}
	h.renderImportStep(w, snapshot)
	return http.StatusOK, len(snapshot.Rows)
}

func (h *Handler) applyImportPanelMapping(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil), -1
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil), -1
	}
	mapping, problems := importMappingFromForm(r, snapshot)
	if len(problems) > 0 {
		h.renderTemplate(w, "import_mapping", newImportMappingView(snapshot, problems))
		return http.StatusOK, -1
	}
	updated, err := h.ImportSession.ApplyMapping(id, mapping)
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), len(updated.Rows)
	}
	h.renderTemplate(w, "import_preview", newImportPreviewView(updated))
	return http.StatusOK, len(updated.Rows)
}

func (h *Handler) applyImportPanelSelection(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil), -1
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil), -1
	}
	updated, err := h.ImportSession.SelectRows(id, importSelectionFromForm(r, len(snapshot.Rows)))
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), -1
	}
	h.renderTemplate(w, "import_commit_bar", newImportPreviewView(updated).CommitBar)
	return http.StatusOK, len(updated.Rows)
}

func (h *Handler) commitImportPanel(w http.ResponseWriter, r *http.Request, id string) int {
	snapshot, ok := h.ImportSession.Snapshot(id)
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "Import session not found", nil)
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil)
	}
	result, err := h.ImportSession.Commit(r.Context(), id, importSelectionFromForm(r, len(snapshot.Rows)))
	if err != nil {
		return h.writeImportStoreError(w, r, id, err)
	}
	h.renderImportCommitted(w, r, snapshot.Kind, result)
	return http.StatusOK
}

func (h *Handler) cancelImportPanel(w http.ResponseWriter, id string) int {
	h.ImportSession.Cancel(id)
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
	w.WriteHeader(http.StatusOK)
	return http.StatusOK
}

func (h *Handler) renderImportCommitted(w http.ResponseWriter, r *http.Request, kind importer.Kind, result importer.CommitResult) {
	view := importCommitView{
		Message:  importCommitMessage(result),
		IsDriver: kind == importer.KindDriver,
	}
	if view.IsDriver {
		view.ListElementID = "drivers-list"
		drivers, err := h.DB.Drivers().List(r.Context(), "")
		if err == nil {
			view.DriverList, err = h.driverListView(r, drivers)
			view.HasList = err == nil
		}
		if err != nil {
			log.Printf("[ERROR] Failed to refresh driver list after import: err=%v", err)
		}
	} else {
		view.ListElementID = "participants-list"
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err == nil {
			view.ParticipantList, err = h.participantListView(r, participants)
			view.HasList = err == nil
		}
		if err != nil {
			log.Printf("[ERROR] Failed to refresh participant list after import: err=%v", err)
		}
	}

	h.setHTMXToast(w, view.Message, toastTypeSuccess)
	h.renderTemplate(w, "import_result", view)
}

func parseImportPanelForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportPanelFormBytes)
	return r.ParseForm()
}
