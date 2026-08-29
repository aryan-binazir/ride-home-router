package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"strconv"
	"strings"
	"testing"
)

type rosterSoftDeleteCase struct {
	name           string
	entityName     string
	livePath       string
	deletedPath    string
	restorePath    string
	notFoundText   string
	emptyStateText string
	retainedText   string
	hasLabels      bool
	create         func(*testing.T, *postgres.Store) int64
	deletePath     func(int64) string
	delete         func(*Handler, http.ResponseWriter, *http.Request)
	listLive       func(*Handler, http.ResponseWriter, *http.Request)
	listDeleted    func(*Handler, http.ResponseWriter, *http.Request)
	restore        func(*Handler, http.ResponseWriter, *http.Request)
}

func TestSoftDeleteRosterHandlers_ListRestoreAndJSON(t *testing.T) {
	for _, tt := range rosterSoftDeleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			exerciseSoftDeleteRosterHandlers(t, tt)
		})
	}
}

func TestRosterPagesRenderSeparateDeletedViews(t *testing.T) {
	tests := []struct {
		name             string
		path             string
		activeContainer  string
		deletedContainer string
		deletedEndpoint  string
		render           func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			name:             "participants",
			path:             "/participants",
			activeContainer:  `id="participants-list"`,
			deletedContainer: `id="participants-deleted"`,
			deletedEndpoint:  `hx-get="/api/v1/participants/deleted"`,
			render:           (*Handler).HandleParticipantsPage,
		},
		{
			name:             "drivers",
			path:             "/drivers",
			activeContainer:  `id="drivers-list"`,
			deletedContainer: `id="drivers-deleted"`,
			deletedEndpoint:  `hx-get="/api/v1/drivers/deleted"`,
			render:           (*Handler).HandleDriversPage,
		},
		{
			name:             "activity_locations",
			path:             "/activity-locations",
			activeContainer:  `id="location-list"`,
			deletedContainer: `id="location-deleted-list"`,
			deletedEndpoint:  `hx-get="/api/v1/activity-locations/deleted"`,
			render:           (*Handler).HandleActivityLocationsPage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _ := newTestManagementHandler(t)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			rr := httptest.NewRecorder()
			tt.render(handler, rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
			}
			for _, want := range []string{tt.activeContainer, tt.deletedContainer, tt.deletedEndpoint, `hx-trigger="rosterRestored from:body"`} {
				if !strings.Contains(rr.Body.String(), want) {
					t.Fatalf("page missing %q, body=%q", want, rr.Body.String())
				}
			}
		})
	}
}

func TestSoftDeleteRosterRestore_NotFoundForLiveAndUnknownIDs(t *testing.T) {
	for _, tt := range rosterSoftDeleteCases() {
		t.Run(tt.name, func(t *testing.T) {
			handler, store := newTestManagementHandler(t)
			liveID := tt.create(t, store)

			for _, idCase := range []struct {
				name string
				id   int64
			}{
				{name: "live", id: liveID},
				{name: "unknown", id: liveID + 1_000_000},
			} {
				for _, htmx := range []bool{true, false} {
					branch := "json"
					if htmx {
						branch = "htmx"
					}
					t.Run(idCase.name+"_"+branch, func(t *testing.T) {
						req := newRestoreRequest(tt.restorePath, idCase.id, htmx)
						rr := httptest.NewRecorder()
						tt.restore(handler, rr, req)

						if rr.Code != http.StatusNotFound {
							t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusNotFound, rr.Body.String())
						}
						contentType := rr.Header().Get("Content-Type")
						if htmx {
							if got := rr.Header().Get("HX-Reswap"); got != "none" {
								t.Fatalf("HX-Reswap = %q, want none", got)
							}
							if got := rr.Header().Get("HX-Trigger"); !strings.Contains(got, tt.notFoundText) {
								t.Fatalf("HX-Trigger = %q, want not-found text %q", got, tt.notFoundText)
							}
						} else if !strings.HasPrefix(contentType, "application/json") {
							t.Fatalf("Content-Type = %q, want application/json", contentType)
						} else if !strings.Contains(rr.Body.String(), tt.notFoundText) {
							t.Fatalf("body = %q, want not-found text %q", rr.Body.String(), tt.notFoundText)
						}
					})
				}
			}
		})
	}
}

func TestSoftDeleteRosterRestore_RejectsNonPositiveIDs(t *testing.T) {
	for _, tt := range rosterSoftDeleteCases() {
		for _, id := range []int64{0, -1} {
			t.Run(fmt.Sprintf("%s_%d", tt.name, id), func(t *testing.T) {
				handler, _ := newTestManagementHandler(t)
				req := newRestoreRequest(tt.restorePath, id, true)
				rr := httptest.NewRecorder()
				tt.restore(handler, rr, req)

				if rr.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
				}
			})
		}
	}
}

func TestSoftDeleteRosterRestore_AllowsLiveDuplicate(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*testing.T, *postgres.Store) (int64, int64)
		assertLive func(*testing.T, *postgres.Store, int64, int64)
		restore    func(*Handler, http.ResponseWriter, *http.Request)
		path       string
	}{
		{
			name: "participant",
			path: "/api/v1/participants/restore",
			prepare: func(t *testing.T, store *postgres.Store) (int64, int64) {
				participant, err := store.Participants().Create(t.Context(), &models.Participant{
					Name: "Duplicate Rider", Address: "1 Duplicate Road", Lat: 40, Lng: -73,
				})
				if err != nil {
					t.Fatalf("create participant: %v", err)
				}
				if err := store.Participants().Delete(t.Context(), participant.ID); err != nil {
					t.Fatalf("archive participant: %v", err)
				}
				imported := &models.Participant{
					Name: participant.Name, Address: participant.Address, Lat: 41, Lng: -72,
				}
				result, err := store.Participants().CreateBatch(t.Context(), []*models.Participant{imported}, nil)
				if err != nil || result.Created != 1 {
					t.Fatalf("import live participant duplicate = %#v, %v", result, err)
				}
				return participant.ID, imported.ID
			},
			assertLive: func(t *testing.T, store *postgres.Store, restoredID, importedID int64) {
				participants, err := store.Participants().List(t.Context(), "")
				if err != nil || len(participants) != 2 {
					t.Fatalf("live participants = %#v, %v; want restored and imported rows", participants, err)
				}
				for _, id := range []int64{restoredID, importedID} {
					if _, err := store.Participants().GetByID(t.Context(), id); err != nil {
						t.Fatalf("GetByID(%d) error = %v; want live participant", id, err)
					}
				}
			},
			restore: (*Handler).HandleRestoreParticipant,
		},
		{
			name: "driver",
			path: "/api/v1/drivers/restore",
			prepare: func(t *testing.T, store *postgres.Store) (int64, int64) {
				driver, err := store.Drivers().Create(t.Context(), &models.Driver{
					Name: "Duplicate Driver", Address: "2 Duplicate Road", Lat: 40, Lng: -73, VehicleCapacity: 4,
				})
				if err != nil {
					t.Fatalf("create driver: %v", err)
				}
				if err := store.Drivers().Delete(t.Context(), driver.ID); err != nil {
					t.Fatalf("archive driver: %v", err)
				}
				imported := &models.Driver{
					Name: driver.Name, Address: driver.Address, Lat: 41, Lng: -72, VehicleCapacity: 6,
				}
				result, err := store.Drivers().CreateBatch(t.Context(), []*models.Driver{imported}, nil)
				if err != nil || result.Created != 1 {
					t.Fatalf("import live driver duplicate = %#v, %v", result, err)
				}
				return driver.ID, imported.ID
			},
			assertLive: func(t *testing.T, store *postgres.Store, restoredID, importedID int64) {
				drivers, err := store.Drivers().List(t.Context(), "")
				if err != nil || len(drivers) != 2 {
					t.Fatalf("live drivers = %#v, %v; want restored and imported rows", drivers, err)
				}
				for _, id := range []int64{restoredID, importedID} {
					if _, err := store.Drivers().GetByID(t.Context(), id); err != nil {
						t.Fatalf("GetByID(%d) error = %v; want live driver", id, err)
					}
				}
			},
			restore: (*Handler).HandleRestoreDriver,
		},
	}

	for _, tt := range tests {
		for _, htmx := range []bool{true, false} {
			branch := "json"
			if htmx {
				branch = "htmx"
			}
			t.Run(tt.name+"_"+branch, func(t *testing.T) {
				handler, store := newTestManagementHandler(t)
				id, importedID := tt.prepare(t, store)
				req := newRestoreRequest(tt.path, id, htmx)
				rr := httptest.NewRecorder()
				tt.restore(handler, rr, req)

				wantStatus := http.StatusNoContent
				if htmx {
					wantStatus = http.StatusOK
				}
				if rr.Code != wantStatus {
					t.Fatalf("status = %d, want %d body=%q", rr.Code, wantStatus, rr.Body.String())
				}
				tt.assertLive(t, store, id, importedID)
			})
		}
	}
}

func exerciseSoftDeleteRosterHandlers(t *testing.T, tt rosterSoftDeleteCase) {
	t.Helper()
	handler, store := newTestManagementHandler(t)
	id := tt.create(t, store)

	deleteReq := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, tt.deletePath(id), nil)
	deleteReq.Header.Set("HX-Request", "true")
	deleteRR := httptest.NewRecorder()
	tt.delete(handler, deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%q", deleteRR.Code, http.StatusOK, deleteRR.Body.String())
	}

	deletedRR := callRosterHandler(tt.listDeleted, handler, http.MethodGet, tt.deletedPath, true)
	if deletedRR.Code != http.StatusOK {
		t.Fatalf("deleted list status = %d, want %d body=%q", deletedRR.Code, http.StatusOK, deletedRR.Body.String())
	}
	for _, want := range []string{tt.entityName, tt.restorePath, "Restore"} {
		if !strings.Contains(deletedRR.Body.String(), want) {
			t.Fatalf("deleted list missing %q, body=%q", want, deletedRR.Body.String())
		}
	}
	if tt.hasLabels {
		if !strings.Contains(deletedRR.Body.String(), tt.retainedText) {
			t.Fatalf("deleted list missing retained label %q, body=%q", tt.retainedText, deletedRR.Body.String())
		}
	} else if strings.Contains(deletedRR.Body.String(), "<th>Labels</th>") {
		t.Fatalf("location deleted list rendered a labels column, body=%q", deletedRR.Body.String())
	}
	if tt.name == "participants" {
		deletedAtPattern := regexp.MustCompile(`[A-Z][a-z]{2} [1-9][0-9]?, 20[0-9]{2} at (1[0-2]|[1-9]):[0-5][0-9] [AP]M UTC`)
		if !deletedAtPattern.MatchString(deletedRR.Body.String()) {
			t.Fatalf("deleted list missing formatted timestamp, body=%q", deletedRR.Body.String())
		}
	}

	liveRR := callRosterHandler(tt.listLive, handler, http.MethodGet, tt.livePath, true)
	if liveRR.Code != http.StatusOK {
		t.Fatalf("live list status = %d, want %d body=%q", liveRR.Code, http.StatusOK, liveRR.Body.String())
	}
	if strings.Contains(liveRR.Body.String(), tt.entityName) {
		t.Fatalf("live list contains archived record %q, body=%q", tt.entityName, liveRR.Body.String())
	}

	deletedJSON := callRosterHandler(tt.listDeleted, handler, http.MethodGet, tt.deletedPath, false)
	if deletedJSON.Code != http.StatusOK {
		t.Fatalf("deleted JSON status = %d, want %d body=%q", deletedJSON.Code, http.StatusOK, deletedJSON.Body.String())
	}
	if !strings.Contains(deletedJSON.Body.String(), `"deleted_at":`) {
		t.Fatalf("deleted JSON missing deleted_at, body=%q", deletedJSON.Body.String())
	}

	restoreReq := newRestoreRequest(tt.restorePath, id, true)
	restoreRR := httptest.NewRecorder()
	tt.restore(handler, restoreRR, restoreReq)
	if restoreRR.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want %d body=%q", restoreRR.Code, http.StatusOK, restoreRR.Body.String())
	}
	if restoreRR.Body.Len() != 0 {
		t.Fatalf("restore body = %q, want empty body", restoreRR.Body.String())
	}
	trigger := restoreRR.Header().Get("HX-Trigger")
	for _, want := range []string{`"rosterRestored":true`, `"type":"success"`} {
		if !strings.Contains(trigger, want) {
			t.Fatalf("HX-Trigger = %q, want %q", trigger, want)
		}
	}

	liveRR = callRosterHandler(tt.listLive, handler, http.MethodGet, tt.livePath, true)
	if !strings.Contains(liveRR.Body.String(), tt.entityName) {
		t.Fatalf("live list missing restored record %q, body=%q", tt.entityName, liveRR.Body.String())
	}
	deletedRR = callRosterHandler(tt.listDeleted, handler, http.MethodGet, tt.deletedPath, true)
	if strings.Contains(deletedRR.Body.String(), tt.entityName) {
		t.Fatalf("deleted list contains restored record %q, body=%q", tt.entityName, deletedRR.Body.String())
	}
	if !strings.Contains(deletedRR.Body.String(), tt.emptyStateText) {
		t.Fatalf("deleted list missing empty state %q, body=%q", tt.emptyStateText, deletedRR.Body.String())
	}
}

func callRosterHandler(
	handlerFunc func(*Handler, http.ResponseWriter, *http.Request),
	handler *Handler,
	method string,
	path string,
	htmx bool,
) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	rr := httptest.NewRecorder()
	handlerFunc(handler, rr, req)
	return rr
}

func newRestoreRequest(path string, id int64, htmx bool) *http.Request {
	form := url.Values{"id": {strconv.FormatInt(id, 10)}}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

func rosterSoftDeleteCases() []rosterSoftDeleteCase {
	return []rosterSoftDeleteCase{
		{
			name:           "participants",
			entityName:     "Archived Rider",
			livePath:       "/api/v1/participants",
			deletedPath:    "/api/v1/participants/deleted",
			restorePath:    "/api/v1/participants/restore",
			notFoundText:   messageParticipantNotFound,
			emptyStateText: "No deleted participants",
			retainedText:   "Needs Ride",
			hasLabels:      true,
			create: func(t *testing.T, store *postgres.Store) int64 {
				t.Helper()
				label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Needs Ride"})
				if err != nil {
					t.Fatalf("create participant label: %v", err)
				}
				participant, err := store.Participants().CreateWithLabels(context.Background(), &models.Participant{
					Name: "Archived Rider", Address: "1 Rider Road", Lat: 40.1, Lng: -73.9,
				}, []int64{label.ID})
				if err != nil {
					t.Fatalf("create participant: %v", err)
				}
				return participant.ID
			},
			deletePath:  func(id int64) string { return "/api/v1/participants/" + strconv.FormatInt(id, 10) },
			delete:      (*Handler).HandleDeleteParticipant,
			listLive:    (*Handler).HandleListParticipants,
			listDeleted: (*Handler).HandleListDeletedParticipants,
			restore:     (*Handler).HandleRestoreParticipant,
		},
		{
			name:           "drivers",
			entityName:     "Archived Driver",
			livePath:       "/api/v1/drivers",
			deletedPath:    "/api/v1/drivers/deleted",
			restorePath:    "/api/v1/drivers/restore",
			notFoundText:   messageDriverNotFound,
			emptyStateText: "No deleted drivers",
			retainedText:   "Evening Driver",
			hasLabels:      true,
			create: func(t *testing.T, store *postgres.Store) int64 {
				t.Helper()
				label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Evening Driver"})
				if err != nil {
					t.Fatalf("create driver label: %v", err)
				}
				driver, err := store.Drivers().CreateWithLabels(context.Background(), &models.Driver{
					Name: "Archived Driver", Address: "2 Driver Drive", Lat: 40.2, Lng: -73.8, VehicleCapacity: 4,
				}, []int64{label.ID})
				if err != nil {
					t.Fatalf("create driver: %v", err)
				}
				return driver.ID
			},
			deletePath:  func(id int64) string { return "/api/v1/drivers/" + strconv.FormatInt(id, 10) },
			delete:      (*Handler).HandleDeleteDriver,
			listLive:    (*Handler).HandleListDrivers,
			listDeleted: (*Handler).HandleListDeletedDrivers,
			restore:     (*Handler).HandleRestoreDriver,
		},
		{
			name:           "activity_locations",
			entityName:     "Archived Gym",
			livePath:       "/api/v1/activity-locations",
			deletedPath:    "/api/v1/activity-locations/deleted",
			restorePath:    "/api/v1/activity-locations/restore",
			notFoundText:   "Activity location not found",
			emptyStateText: "No deleted locations",
			create: func(t *testing.T, store *postgres.Store) int64 {
				t.Helper()
				location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
					Name: "Archived Gym", Address: "3 Gym Lane", Lat: 40.3, Lng: -73.7,
				})
				if err != nil {
					t.Fatalf("create activity location: %v", err)
				}
				return location.ID
			},
			deletePath:  func(id int64) string { return "/api/v1/activity-locations/" + strconv.FormatInt(id, 10) },
			delete:      (*Handler).HandleDeleteActivityLocation,
			listLive:    (*Handler).HandleListActivityLocations,
			listDeleted: (*Handler).HandleListDeletedActivityLocations,
			restore:     (*Handler).HandleRestoreActivityLocation,
		},
	}
}

type archiveLocationAfterGetStore struct {
	database.DataStore
}

func (s archiveLocationAfterGetStore) ActivityLocations() database.ActivityLocationRepository {
	return archiveLocationAfterGetRepository{ActivityLocationRepository: s.DataStore.ActivityLocations()}
}

type archiveLocationAfterGetRepository struct {
	database.ActivityLocationRepository
}

func (r archiveLocationAfterGetRepository) GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error) {
	location, err := r.ActivityLocationRepository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.Delete(ctx, id); err != nil {
		return nil, fmt.Errorf("archive location after get: %w", err)
	}
	return location, nil
}

func TestHandleUpdateSettings_LocationArchivedBeforeWriteReturnsNotFound(t *testing.T) {
	for _, htmx := range []bool{true, false} {
		name := "json"
		if htmx {
			name = "htmx"
		}
		t.Run(name, func(t *testing.T) {
			handler, store := newTestManagementHandler(t)
			location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
				Name: "Race Location", Address: "4 Race Road", Lat: 40.4, Lng: -73.6,
			})
			if err != nil {
				t.Fatalf("create activity location: %v", err)
			}
			handler.DB = archiveLocationAfterGetStore{DataStore: store}

			var req *http.Request
			if htmx {
				form := url.Values{"selected_activity_location_id": {strconv.FormatInt(location.ID, 10)}}
				req = httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.Header.Set("HX-Request", "true")
			} else {
				body := fmt.Sprintf(`{"selected_activity_location_id":%d,"use_miles":true}`, location.ID)
				req = httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			handler.HandleUpdateSettings(rr, req)

			if rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusNotFound, rr.Body.String())
			}
			if htmx {
				if !strings.Contains(rr.Header().Get("HX-Trigger"), messageSelectedActivityLocationNotFound) {
					t.Fatalf("HX-Trigger = %q, want not-found toast", rr.Header().Get("HX-Trigger"))
				}
				if got := rr.Header().Get("HX-Reswap"); got != "none" {
					t.Fatalf("HX-Reswap = %q, want none", got)
				}
			} else if !strings.Contains(rr.Body.String(), `"code":"NOT_FOUND"`) || !strings.Contains(rr.Body.String(), messageSelectedActivityLocationNotFound) {
				t.Fatalf("body = %q, want JSON not-found response", rr.Body.String())
			}
		})
	}
}
