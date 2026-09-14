package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/importer"
	"strconv"
	"strings"
	"testing"
)

func TestImportPagingAppliesVisibleDeltasAndPollsOnlyProgress(t *testing.T) {
	h, db := newImportTestHandler(t, &importTestGeocoder{})
	csv := "name,address\n"
	for i := 1; i <= 60; i++ {
		csv += fmt.Sprintf("Rider %03d,1 Main St\n", i)
	}
	w := httptest.NewRecorder()
	h.HandleCreateImport(w, newImportPanelUploadRequest(t, "riders.csv", csv, importer.KindParticipant, ""))
	id := importPanelSessionID(t, w.Body.String())
	w = httptest.NewRecorder()
	h.HandleImportSession(w, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	if strings.Count(w.Body.String(), `class="import-row`) != 50 || strings.Contains(w.Body.String(), "Rider 060") {
		t.Fatal("unbounded initial review")
	}
	w = httptest.NewRecorder()
	h.HandleImportSession(w, newImportPanelRequest(http.MethodGet, "/api/v1/imports/"+id+"?view=panel&progress=1"))
	if strings.Contains(w.Body.String(), "<table") || strings.Contains(w.Body.String(), "Rider 001") || len(w.Body.Bytes()) > 5000 {
		t.Fatal("poll includes review rows")
	}
	waitForImportHTTPGeocoding(t, h, id)
	values := url.Values{"page_selection": {"1"}}
	for i := range 50 {
		values.Add("visible", strconv.Itoa(i))
		if i != 0 {
			values.Add("selected", strconv.Itoa(i))
		}
	}
	w = httptest.NewRecorder()
	h.HandleImportSession(w, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/selection?view=panel&page=1&offset=50", values))
	if strings.Count(w.Body.String(), `class="import-row`) != 10 || !strings.Contains(w.Body.String(), "59 of 60 rows selected") {
		t.Fatalf("page selection response: %s", w.Body.String())
	}
	values = url.Values{"page_selection": {"1"}}
	for i := 50; i < 60; i++ {
		values.Add("visible", strconv.Itoa(i))
		if i != 59 {
			values.Add("selected", strconv.Itoa(i))
		}
	}
	w = httptest.NewRecorder()
	h.HandleImportSession(w, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", values))
	people, err := db.Participants().List(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 58 {
		t.Fatalf("committed %d rows, response=%s", len(people), w.Body.String())
	}
}

func TestImportPreviewBoundsRowsButCountsAllSelections(t *testing.T) {
	snapshot := importer.Snapshot{Status: importer.StatusPreviewing, Rows: make([]importer.Row, 2000), Selected: make([]bool, 2000)}
	for i := range snapshot.Selected {
		snapshot.Selected[i] = true
	}
	view := newImportPreviewView(snapshot)
	if len(view.Rows) != 50 || view.CommitBar.Selected != 2000 || view.CommitBar.Total != 2000 {
		t.Fatalf("preview rows=%d selected=%d total=%d", len(view.Rows), view.CommitBar.Selected, view.CommitBar.Total)
	}
}
