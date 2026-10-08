package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestImportPanelCommitLoadsStagedRowsOnce(t *testing.T) {
	const rowCount = 2000
	firstVisible := rowCount - importer.ReviewPageSize
	db := postgrestest.Open(t)
	observed := &commitPayloadObserver{WorkflowRepository: db.Workflows()}
	geocoder := &importTestGeocoder{}
	store := importer.NewPersistentStore(t.Context(), geocoder, db, observed, commitObservedJobs{ImportJobRepository: db.ImportJobs(), observer: observed})
	t.Cleanup(store.Close)
	h := &Handler{DB: db, Geocoder: geocoder, ImportSession: store, Renderer: loadEmbeddedTemplates(t)}
	var csv strings.Builder
	csv.WriteString("name,address\n")
	for i := range rowCount {
		name := fmt.Sprintf("Rider %04d", i)
		if i == rowCount-1 {
			name = ""
		}
		fmt.Fprintf(&csv, "%s,1 Main St\n", name)
	}
	id := startImportPanelSession(t, h, csv.String(), importer.KindParticipant)
	mapped := httptest.NewRecorder()
	h.HandleImportSession(mapped, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	assertPanelFragment(t, mapped)
	deadline := time.Now().Add(5 * time.Second)
	for {
		progress, ok, err := store.LoadProgress(t.Context(), id)
		if err != nil || !ok {
			t.Fatalf("wait for import: found=%t err=%v", ok, err)
		}
		if !progress.GeocodeProgress.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("large import geocoding did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := store.SelectRowsPatch(t.Context(), id, map[int]bool{0: false}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"page_selection": {"1"}}
	for i := firstVisible; i < rowCount; i++ {
		form.Add("visible", fmt.Sprint(i))
		if i != firstVisible {
			form.Add("selected", fmt.Sprint(i))
		}
	}
	observed.payloadRows.Store(0)
	observed.fullReads.Store(0)
	response := httptest.NewRecorder()
	h.HandleImportSession(response, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", form))
	assertPanelFragment(t, response)
	if body := response.Body.String(); !strings.Contains(body, fmt.Sprintf("%d imported, 0 updated, 3 skipped", rowCount-3)) {
		t.Fatalf("commit result: %s", body)
	}
	if got := observed.payloadRows.Load(); got != rowCount {
		t.Fatalf("commit transferred %d row payloads, want one read of %d", got, rowCount)
	}
	if got := observed.fullReads.Load(); got != 1 {
		t.Fatalf("commit full-row reads = %d, want 1", got)
	}
	t.Logf("panel commit: staged=%d payload rows=%d full-row reads=%d", rowCount, observed.payloadRows.Load(), observed.fullReads.Load())
	roster, err := db.Participants().List(t.Context(), "")
	if err != nil || len(roster) != rowCount-3 {
		t.Fatalf("committed roster count=%d err=%v", len(roster), err)
	}
	for _, rider := range roster {
		if rider.Name == "Rider 0000" || rider.Name == fmt.Sprintf("Rider %04d", firstVisible) {
			t.Fatalf("committed unchecked row %q", rider.Name)
		}
	}

}

type commitPayloadObserver struct {
	database.WorkflowRepository
	payloadRows atomic.Int64
	fullReads   atomic.Int64
}

func (o *commitPayloadObserver) Transact(ctx context.Context, kind, id string, ttl time.Duration, update func(*database.WorkflowRecord, database.WorkflowWrites) error) error {
	return o.WorkflowRepository.Transact(ctx, kind, id, ttl, func(record *database.WorkflowRecord, writes database.WorkflowWrites) error {
		return update(record, commitObservedWrites{WorkflowWrites: writes, observer: o})
	})
}

type commitObservedWrites struct {
	database.WorkflowWrites
	observer *commitPayloadObserver
}

func (w commitObservedWrites) ImportRows(ctx context.Context, id string) ([]database.ImportRow, error) {
	rows, err := w.WorkflowWrites.ImportRows(ctx, id)
	w.observer.payloadRows.Add(int64(len(rows)))
	w.observer.fullReads.Add(1)
	return rows, err
}

func (w commitObservedWrites) ImportRowsByIndices(ctx context.Context, id string, indices []int) ([]database.ImportRow, error) {
	rows, err := w.WorkflowWrites.ImportRowsByIndices(ctx, id, indices)
	w.observer.payloadRows.Add(int64(len(rows)))
	return rows, err
}

type commitObservedJobs struct {
	database.ImportJobRepository
	observer *commitPayloadObserver
}

func (j commitObservedJobs) Rows(ctx context.Context, id string) ([]database.ImportRow, error) {
	rows, err := j.ImportJobRepository.Rows(ctx, id)
	j.observer.payloadRows.Add(int64(len(rows)))
	j.observer.fullReads.Add(1)
	return rows, err
}

func (j commitObservedJobs) RowsByIndices(ctx context.Context, id string, indices []int) ([]database.ImportRow, error) {
	rows, err := j.ImportJobRepository.RowsByIndices(ctx, id, indices)
	j.observer.payloadRows.Add(int64(len(rows)))
	return rows, err
}

func TestImportPanelCommitPreservesErrorPrecedence(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, databaseURL)
	pause := make(chan struct{})
	geocoder := &importTestGeocoder{pause: pause}
	store := importer.NewPersistentStore(t.Context(), geocoder, db, db.Workflows(), db.ImportJobs())
	t.Cleanup(store.Close)
	h := &Handler{DB: db, Geocoder: geocoder, ImportSession: store, Renderer: loadEmbeddedTemplates(t)}
	mapping := startImportPanelSession(t, h, "name,address\nMapping,1 Main St\n", importer.KindParticipant)
	pending := startImportPanelSession(t, h, "name,address\nPending,1 Main St\n", importer.KindParticipant)
	mapped := httptest.NewRecorder()
	h.HandleImportSession(mapped, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+pending+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	assertPanelFragment(t, mapped)
	expired := startImportPanelSession(t, h, "name,address\nExpired,1 Main St\n", importer.KindParticipant)
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(t.Context(), `UPDATE workflow_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE kind='import' AND id=$1`, expired); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ name, id, body, message string }{
		{"missing malformed", "0123456789abcdef0123456789abcdef", "selected=%zz", "That import expired"},
		{"expired malformed", expired, "selected=%zz", "That import expired"},
		{"mapping malformed", mapping, "selected=%zz", "That request could not be read"},
		{"pending malformed", pending, "selected=%zz", "That request could not be read"},
		{"mapping invalid page", mapping, "page_selection=1&visible=0&selected=0", "Choose valid rows"},
		{"pending invalid page", pending, "page_selection=1&visible=0&visible=1&selected=0", "Choose valid rows"},
		{"pending malformed index", pending, "page_selection=1&visible=oops", "Choose valid rows"},
		{"pending selected outside page", pending, "page_selection=1&visible=0&selected=1", "Choose valid rows"},
		{"pending valid page", pending, "page_selection=1&visible=0&selected=0", "Addresses are still being checked"},
		{"mapping valid form", mapping, "", "That import changed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			live := tt.id == mapping || tt.id == pending
			if live {
				if _, err := conn.Exec(t.Context(), `UPDATE workflow_sessions SET expires_at=clock_timestamp()+interval '1 minute' WHERE kind='import' AND id=$1`, tt.id); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/imports/"+tt.id+"/commit?view=panel", strings.NewReader(tt.body))
			req.Host = "localhost:8080"
			req.Header.Set("HX-Request", "true")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			h.HandleImportSession(response, req)
			assertPanelFragment(t, response)
			if !strings.Contains(response.Body.String(), tt.message) {
				t.Fatalf("expected %q: %s", tt.message, response.Body.String())
			}
			if live {
				var renewed bool
				if err := conn.QueryRow(t.Context(), `SELECT expires_at>clock_timestamp()+interval '2 minutes' FROM workflow_sessions WHERE kind='import' AND id=$1`, tt.id).Scan(&renewed); err != nil || !renewed {
					t.Fatalf("failed commit did not renew sliding expiry: renewed=%t err=%v", renewed, err)
				}
			}
		})
	}
	close(pause)
	waitForImportHTTPGeocoding(t, h, pending)
	saved := httptest.NewRecorder()
	h.HandleImportSession(saved, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+pending+"/commit?view=panel", url.Values{"selected": {"0"}}))
	assertPanelFragment(t, saved)
	for _, tt := range []struct{ body, message string }{
		{"selected=%zz", "That request could not be read"},
		{"page_selection=1&visible=0&selected=0", "Choose valid rows"},
		{"", "already been saved"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/imports/"+pending+"/commit?view=panel", strings.NewReader(tt.body))
		req.Host = "localhost:8080"
		req.Header.Set("HX-Request", "true")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		h.HandleImportSession(response, req)
		assertPanelFragment(t, response)
		if !strings.Contains(response.Body.String(), tt.message) {
			t.Fatalf("consumed: expected %q: %s", tt.message, response.Body.String())
		}
	}
}

func TestImportPanelCommitAcknowledgesSaveWhenRefreshFails(t *testing.T) {
	for _, kind := range []importer.Kind{importer.KindParticipant, importer.KindDriver} {
		t.Run(string(kind), func(t *testing.T) {
			h, db := newImportTestHandler(t, &importTestGeocoder{})
			id := startImportPanelSession(t, h, "name,address\nSaved,1 Main St\n", kind)
			mapped := httptest.NewRecorder()
			h.HandleImportSession(mapped, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
			assertPanelFragment(t, mapped)
			waitForImportHTTPGeocoding(t, h, id)
			h.DB = rosterRefreshFailureStore{DataStore: db, participants: failedParticipantList{db.Participants()}, drivers: failedDriverList{db.Drivers()}, labels: db.Labels()}
			response := httptest.NewRecorder()
			h.HandleImportSession(response, newImportPanelFormRequest(http.MethodPost, "/api/v1/imports/"+id+"/commit?view=panel", url.Values{"selected": {"0", "garbage", "-1", "1"}}))
			assertPanelFragment(t, response)
			if !strings.Contains(response.Body.String(), "1 imported, 0 updated") || strings.Contains(response.Body.String(), `hx-swap-oob="innerHTML"`) {
				t.Fatalf("save acknowledgment: %s", response.Body.String())
			}
			var trigger struct {
				ShowToast htmxToast `json:"showToast"`
			}
			if err := json.Unmarshal([]byte(response.Header().Get("HX-Trigger")), &trigger); err != nil || trigger.ShowToast.Type != toastTypeSuccess {
				t.Fatalf("save toast: %+v %v", trigger, err)
			}
			snapshot, ok, err := h.ImportSession.Load(t.Context(), id)
			if err != nil || !ok || snapshot.Status != importer.StatusCommitted || snapshot.Kind != kind || snapshot.CommitResult.Created != 1 {
				t.Fatalf("saved import: %+v %v", snapshot, err)
			}
		})
	}
}
