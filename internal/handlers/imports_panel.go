package handlers

import (
	"fmt"
	"log"
	"net/http"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/importer"
	"strconv"
	"strings"
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
	OOB       bool
	SessionID string
	Selected  int
	Total     int
	Disabled  bool
}

type importPreviewView struct {
	Offset, Next, Previous int
	ProgressOnly           bool
	SessionID              string
	Filename               string
	IsDriver               bool
	Warnings               []string
	Rows                   []importRowView
	Geocoding              bool
	GeocodeDone            int
	GeocodeTotal           int
	CommitBar              importCommitBarView
}

type importMessageView struct {
	SessionID string
	Message   string
}

type importSheetsView struct {
	Sheets []string
}

type importCommitView struct {
	Message string
	// GuessedWarning is shown only when the geocoder guessed some addresses.
	GuessedWarning  string
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
		{importer.FieldCapacity, mapping.CapacityColumn},
	}
}

// importMappingFromForm reads one dropdown per file column. The panel form is
// authoritative for every column, so an unlisted column is ignored and no
// ambiguity survives the round trip.
func importMappingFromForm(r *http.Request, snapshot importer.Snapshot) (importer.Mapping, []string) {
	isDriver := snapshot.Kind == importer.KindDriver
	assignments := make([]importer.FieldColumn, 0, len(snapshot.Grid.Headers))
	for column := range snapshot.Grid.Headers {
		field, ok := importFieldFromValue(r.FormValue(fmt.Sprintf("column_%d", column)), isDriver)
		if ok {
			assignments = append(assignments, importer.FieldColumn{Field: field, Column: column})
		}
	}

	transition := importer.NewMapping().Assign(assignments, len(snapshot.Grid.Headers))
	var problems []string
	for _, field := range transition.DuplicateFields {
		problems = append(problems, fmt.Sprintf("%s is mapped to more than one column — pick one.", importFieldLabel(field)))
	}
	for _, field := range transition.MissingRequired {
		problems = append(problems, fmt.Sprintf("Choose a column for %s.", importFieldLabel(field)))
	}
	return transition.Mapping, problems
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

func importPageSelection(r *http.Request, rowCount int) (map[int]bool, error) {
	if len(r.Form["visible"]) > rosterPageSize {
		return nil, importer.ErrInvalidSelection
	}
	patch := make(map[int]bool, len(r.Form["visible"]))
	for _, value := range r.Form["visible"] {
		index, err := strconv.Atoi(value)
		if err != nil || index < 0 || index >= rowCount {
			return nil, importer.ErrInvalidSelection
		}
		patch[index] = false
	}
	for _, value := range r.Form["selected"] {
		index, err := strconv.Atoi(value)
		if err != nil {
			return nil, importer.ErrInvalidSelection
		}
		if _, ok := patch[index]; !ok {
			return nil, importer.ErrInvalidSelection
		}
		patch[index] = true
	}
	return patch, nil
}

func newImportPreviewView(snapshot importer.Snapshot) importPreviewView {
	return importPreviewPage(snapshot, 0)
}

func importPreviewPage(snapshot importer.Snapshot, offset int) importPreviewView {
	isDriver := snapshot.Kind == importer.KindDriver
	start, end, next, previous := pickerWindow(len(snapshot.Rows), offset)
	rows := make([]importRowView, 0, end-start)
	for index := start; index < end; index++ {
		row := snapshot.Rows[index]
		rows = append(rows, importRowView{
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
		})
	}

	// Geocoding can make a previously selected row unselectable.
	selectedCount := 0
	for i, row := range snapshot.Rows {
		if i < len(snapshot.Selected) && snapshot.Selected[i] && len(row.Errors) == 0 {
			selectedCount++
		}
	}
	geocoding := snapshot.GeocodeProgress.Running

	return importPreviewView{
		Offset: start, Next: next, Previous: previous,
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
	case len(row.Warnings) > 0, row.AddressGuessed:
		return "warning"
	default:
		return ""
	}
}

// importGuessedNote is the preview note for an address the geocoder only guessed.
func importGuessedNote(matched string) string {
	return "Google couldn't find this exactly. Matched to: " + matched + ". Check this."
}

func importRowNotes(row importer.Row) []string {
	notes := make([]string, 0, len(row.Errors)+len(row.Warnings)+2)
	notes = append(notes, row.Errors...)
	if row.DuplicateOfExisting {
		notes = append(notes, "Already in your roster — will be updated")
	}
	if row.DuplicateInFile {
		notes = append(notes, "Duplicate row in this file — merged into the first")
	}
	if row.AddressGuessed {
		notes = append(notes, importGuessedNote(row.MatchedAddress))
	}
	return append(notes, row.Warnings...)
}

func importCommitMessage(result importer.CommitResult) string {
	return fmt.Sprintf("%d imported, %d updated, %d skipped", result.Created, result.Updated, result.NotSelected)
}

// importGuessedWarning tells people to check addresses the geocoder guessed;
// empty when every address matched exactly.
func importGuessedWarning(result importer.CommitResult) string {
	switch {
	case result.Guessed <= 0:
		return ""
	case result.Guessed == 1:
		return "We couldn't confirm 1 address exactly, so we used Google's closest match. Look for the red marker next to that name and check it. Click the marker to see what we matched and confirm it or fix it."
	default:
		return fmt.Sprintf("We couldn't confirm %d addresses exactly, so we used Google's closest match. Look for the red marker next to those names and check each one. Click the marker to see what we matched and confirm it or fix it.", result.Guessed)
	}
}

// writeImportError returns the emitted status; HTMX errors use 200 so htmx swaps.
func (h *Handler) writeImportError(w http.ResponseWriter, r *http.Request, sessionID string, status int, code, message string, details any) int {
	if status == http.StatusTooManyRequests {
		h.handleHTMXErrorNoSwap(w, r, status, code, message)
		return status
	}
	if h.wantsImportPanel(r) {
		h.setHTMXToast(w, message, toastTypeError)
		h.renderImportMessage(w, sessionID, message)
		return http.StatusOK
	}
	h.writeError(w, r, status, code, message, details)
	return status
}

func (h *Handler) renderImportStep(w http.ResponseWriter, r *http.Request, snapshot importer.Snapshot) {
	switch snapshot.Status {
	case importer.StatusMapping:
		h.renderTemplate(w, "import_mapping", newImportMappingView(snapshot, nil))
	case importer.StatusPreviewing, importer.StatusCommitting:
		h.renderImportPreview(w, r, snapshot)
	case importer.StatusCommitted:
		h.renderTemplate(w, "import_result", importCommitView{Message: importCommitMessage(snapshot.CommitResult), GuessedWarning: importGuessedWarning(snapshot.CommitResult)})
	case importer.StatusFailed:
		h.renderImportMessage(w, snapshot.ID, importFailureMessage(snapshot))
	default:
		h.renderImportMessage(w, snapshot.ID, importFailureMessage(snapshot))
	}
}

func (h *Handler) renderImportPreview(w http.ResponseWriter, r *http.Request, snapshot importer.Snapshot) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	view := importPreviewPage(snapshot, offset)
	if r.URL.Query().Get("progress") == "1" {
		view.ProgressOnly = true
		view.CommitBar.OOB = true
		h.renderTemplate(w, "import_progress", view)
		return
	}
	if view.Geocoding {
		configured, err := h.DB.Settings().GoogleMapsKeyConfigured(r.Context())
		if err != nil {
			log.Print("[IMPORT] Unable to check address lookup configuration")
		} else if !configured {
			view.Warnings = append(view.Warnings, "Address lookup is not configured. Ask an administrator to add the Google Maps key in Settings. This import will resume automatically.")
		}
	}
	h.renderTemplate(w, "import_preview", view)
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
	if r.URL.Query().Get("progress") == "1" {
		progress, ok, err := h.ImportSession.LoadProgress(r.Context(), id)
		if err != nil {
			return h.writeImportStoreError(w, r, id, err), -1
		}
		if !ok {
			return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "That import expired. Choose your file again.", nil), -1
		}
		if progress.Status == importer.StatusPreviewing || progress.Status == importer.StatusCommitting {
			requested, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			offset, _, _, _ := pickerWindow(progress.RowCount, requested)
			view := importPreviewView{
				Offset: offset, ProgressOnly: true, SessionID: id,
				Geocoding:   progress.GeocodeProgress.Running,
				GeocodeDone: progress.GeocodeProgress.Done, GeocodeTotal: progress.GeocodeProgress.Total,
				CommitBar: importCommitBarView{
					OOB: true, SessionID: id, Selected: progress.SelectedCount, Total: progress.RowCount,
					Disabled: progress.GeocodeProgress.Running || progress.SelectedCount == 0 || progress.Status != importer.StatusPreviewing,
				},
			}
			h.renderTemplate(w, "import_progress", view)
			return http.StatusOK, progress.RowCount
		}
	}
	snapshot, ok, loadErr := h.ImportSession.Load(r.Context(), id)
	if loadErr != nil {
		return h.writeImportStoreError(w, r, id, loadErr), -1
	}
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "That import expired. Choose your file again.", nil), -1
	}
	if r.URL.Query().Get("progress") == "1" && snapshot.Status != importer.StatusPreviewing && snapshot.Status != importer.StatusCommitting {
		w.Header().Set("HX-Retarget", "#import-steps")
		w.Header().Set("HX-Reswap", "innerHTML")
	}
	h.renderImportStep(w, r, snapshot)
	return http.StatusOK, len(snapshot.Rows)
}

func (h *Handler) applyImportPanelMapping(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok, loadErr := h.ImportSession.Load(r.Context(), id)
	if loadErr != nil {
		return h.writeImportStoreError(w, r, id, loadErr), -1
	}
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "That import expired. Choose your file again.", nil), -1
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil), -1
	}
	mapping, problems := importMappingFromForm(r, snapshot)
	if len(problems) > 0 {
		view := newImportMappingView(snapshot, problems)
		for i := range view.Columns {
			value := r.FormValue(fmt.Sprintf("column_%d", view.Columns[i].Index))
			view.Columns[i].Selected = importIgnoreValue
			if field, ok := importFieldFromValue(value, view.IsDriver); ok {
				view.Columns[i].Selected = string(field)
			}
		}
		h.renderTemplate(w, "import_mapping", view)
		return http.StatusOK, -1
	}
	updated, err := h.ImportSession.ApplyMapping(r.Context(), id, mapping)
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), len(updated.Rows)
	}
	h.renderImportPreview(w, r, updated)
	return http.StatusOK, len(updated.Rows)
}

func (h *Handler) applyImportPanelSelection(w http.ResponseWriter, r *http.Request, id string) (int, int) {
	snapshot, ok, loadErr := h.ImportSession.Load(r.Context(), id)
	if loadErr != nil {
		return h.writeImportStoreError(w, r, id, loadErr), -1
	}
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "That import expired. Choose your file again.", nil), -1
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil), -1
	}
	var updated importer.Snapshot
	var err error
	if r.Form.Get("page_selection") == "1" {
		var patch map[int]bool
		patch, err = importPageSelection(r, len(snapshot.Rows))
		if err == nil {
			updated, err = h.ImportSession.SelectRowsPatch(r.Context(), id, patch)
		}
	} else {
		updated, err = h.ImportSession.SelectRowsContext(r.Context(), id, importSelectionFromForm(r, len(snapshot.Rows)))
	}
	if err != nil {
		return h.writeImportStoreError(w, r, id, err), -1
	}
	if r.URL.Query().Get("page") == "1" {
		h.renderImportPreview(w, r, updated)
	} else {
		h.renderTemplate(w, "import_commit_bar", newImportPreviewView(updated).CommitBar)
	}
	return http.StatusOK, len(updated.Rows)
}

func (h *Handler) commitImportPanel(w http.ResponseWriter, r *http.Request, id string) int {
	snapshot, ok, loadErr := h.ImportSession.Load(r.Context(), id)
	if loadErr != nil {
		return h.writeImportStoreError(w, r, id, loadErr)
	}
	if !ok {
		return h.writeImportError(w, r, id, http.StatusNotFound, "NOT_FOUND", "That import expired. Choose your file again.", nil)
	}
	if err := parseImportPanelForm(w, r); err != nil {
		return h.writeImportError(w, r, id, http.StatusBadRequest, "INVALID_REQUEST_BODY", messageInvalidRequestBody, nil)
	}
	var result importer.CommitResult
	var err error
	if r.Form.Get("page_selection") == "1" {
		var patch map[int]bool
		patch, err = importPageSelection(r, len(snapshot.Rows))
		if err == nil {
			result, err = h.ImportSession.CommitRowsPatch(r.Context(), id, patch)
		}
	} else {
		result, err = h.ImportSession.Commit(r.Context(), id, importSelectionFromForm(r, len(snapshot.Rows)))
	}
	if err != nil {
		return h.writeImportStoreError(w, r, id, err)
	}
	h.renderImportCommitted(w, r, snapshot.Kind, result)
	return http.StatusOK
}

func (h *Handler) cancelImportPanel(w http.ResponseWriter, r *http.Request, id string) int {
	if _, err := h.ImportSession.CancelContext(r.Context(), id); err != nil {
		return h.writeImportStoreError(w, r, id, err)
	}
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
	w.WriteHeader(http.StatusOK)
	return http.StatusOK
}

func (h *Handler) renderImportCommitted(w http.ResponseWriter, r *http.Request, kind importer.Kind, result importer.CommitResult) {
	view := importCommitView{
		Message:        importCommitMessage(result),
		GuessedWarning: importGuessedWarning(result),
		IsDriver:       kind == importer.KindDriver,
	}
	if view.IsDriver {
		view.ListElementID = "drivers-list"
		drivers, err := h.DB.Drivers().List(r.Context(), strings.TrimSpace(r.FormValue("search")))
		if err == nil {
			view.DriverList, err = h.driverListView(r, drivers)
			view.HasList = err == nil
		}
		if err != nil {
			log.Printf("[ERROR] Failed to refresh driver list after import: err=%v", err)
		}
	} else {
		view.ListElementID = "participants-list"
		participants, err := h.DB.Participants().List(r.Context(), strings.TrimSpace(r.FormValue("search")))
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
