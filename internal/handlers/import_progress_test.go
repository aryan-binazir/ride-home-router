package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type progressOnlyJobs struct {
	database.ImportJobRepository
	rejectRows atomic.Bool
}

func (j *progressOnlyJobs) Rows(ctx context.Context, id string) ([]database.ImportRow, error) {
	if j.rejectRows.Load() {
		return nil, errors.New("progress downloaded row payloads")
	}
	return j.ImportJobRepository.Rows(ctx, id)
}

type progressGeocoder struct {
	geocoding.Geocoder
	release chan struct{}
}

func (g *progressGeocoder) GeocodeWithRetry(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.release:
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 35, Lng: -79}}, nil
	}
}

func TestPersistentImportProgressPanelUsesOnlyCounts(t *testing.T) {
	db := postgrestest.Open(t)
	jobs := &progressOnlyJobs{ImportJobRepository: db.ImportJobs()}
	g := &progressGeocoder{release: make(chan struct{})}
	store := importer.NewPersistentStore(t.Context(), g, db, db.Workflows(), jobs)
	defer store.Close()
	h := &Handler{DB: db, ImportSession: store, Renderer: loadEmbeddedTemplates(t)}
	var csv strings.Builder
	csv.WriteString("name,address\n")
	for i := range 60 {
		fmt.Fprintf(&csv, "Rider %d,1 Shared St\n", i)
	}
	csv.WriteString(",Invalid row\n")
	id := startImportPanelSession(t, h, csv.String(), importer.KindParticipant)
	poll := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.HandleImportSession(w, newImportPanelRequest(http.MethodGet, "/api/v1/imports/"+id+"?view=panel&progress=1&offset=50"))
		return w
	}
	mapping := poll()
	if mapping.Code != 200 || mapping.Header().Get("HX-Retarget") != "#import-steps" || !strings.Contains(mapping.Body.String(), "Continue") {
		t.Fatalf("mapping fallback: %d %s", mapping.Code, mapping.Body.String())
	}
	w := httptest.NewRecorder()
	h.HandleImportSession(w, newImportPanelFormRequest(http.MethodPut, "/api/v1/imports/"+id+"/mapping?view=panel", url.Values{"column_0": {"name"}, "column_1": {"address"}}))
	assertPanelFragment(t, w)
	jobs.rejectRows.Store(true)
	w = poll()
	assertPanelFragment(t, w)
	for _, want := range []string{"60 of 61 rows selected", "offset=50", "every 2s", "disabled>", `hx-swap-oob="outerHTML"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("pending progress missing %q: %s", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), `class="import-row`) {
		t.Fatal("progress rendered preview rows")
	}
	close(g.release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		done, total, err := db.ImportJobs().Progress(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if done == 1 && total == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job unfinished %d/%d", done, total)
		}
		time.Sleep(10 * time.Millisecond)
	}
	w = poll()
	assertPanelFragment(t, w)
	for _, want := range []string{"60 of 61 rows selected", "offset=50", `hx-trigger="load, importResume"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("finished progress missing %q: %s", want, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "disabled>") || strings.Contains(w.Body.String(), "every 2s") {
		t.Fatalf("finished progress still pending: %s", w.Body.String())
	}
	if _, err := store.Commit(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	jobs.rejectRows.Store(false)
	w = poll()
	if w.Code != 200 || w.Header().Get("HX-Retarget") != "#import-steps" || !strings.Contains(w.Body.String(), "60 imported") {
		t.Fatalf("committed fallback: %d %s", w.Code, w.Body.String())
	}
}
