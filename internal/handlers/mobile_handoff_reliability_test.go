package handlers

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func mobileRoutePage(t *testing.T, h *Handler, draftID string) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/routes", nil)
	req.AddCookie(mobileTestCookie(draftID))
	response := httptest.NewRecorder()
	h.HandleMobileRoutes(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("routes: %d %s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func handoffTextarea(t *testing.T, page, id string) string {
	t.Helper()
	pattern := regexp.MustCompile(`<textarea[^>]*id="` + regexp.QuoteMeta(id) + `"[^>]*>([\s\S]*?)</textarea>`)
	match := pattern.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("missing textarea %s", id)
	}
	return html.UnescapeString(match[1])
}

func TestMobileMapsHandoffPreservesStopsAcrossSupportedLegs(t *testing.T) {
	for _, mode := range []models.RouteMode{models.RouteModePickup, models.RouteModeDropoff} {
		for _, count := range []int{1, 4, 10} {
			t.Run(fmt.Sprintf("%s/%d", mode, count), func(t *testing.T) {
				h, _ := newTestManagementHandler(t)
				h.PlanDraft = plandraft.NewStore()
				t.Cleanup(h.PlanDraft.Close)
				route := models.CalculatedRoute{Driver: &models.Driver{ID: 1, Name: "Driver", Address: "Driver home", Lat: 41, Lng: -73, VehicleCapacity: 12}, EffectiveCapacity: 12, Mode: mode}
				var expected []string
				for i := 1; i <= count; i++ {
					lat := 40 + float64(i)/100
					if i == 3 {
						lat = 40.01 // A later visit to the first address must not disappear.
					}
					route.Stops = append(route.Stops, models.RouteStop{Participant: &models.Participant{ID: int64(i), Name: fmt.Sprintf("Rider %d", i), Lat: lat, Lng: -73}})
					expected = append(expected, fmt.Sprintf("%.6f,-73.000000", lat))
				}
				if mode == models.RouteModePickup {
					expected = append(expected, "40.000000,-73.000000")
				} else {
					expected = append(expected, "41.000000,-73.000000")
				}
				snapshot := h.RouteSession.Create(routesession.CreateInput{Routes: []models.CalculatedRoute{route}, Mode: mode, RouteTime: "18:30", ActivityLocation: &models.ActivityLocation{Name: "Activity", Address: "Activity Road", Lat: 40, Lng: -73}})
				id := h.PlanDraft.NewID()
				h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = snapshot.ID })
				text := handoffTextarea(t, mobileRoutePage(t, h, id), "driver-copy-0")
				var visited []string
				previous := ""
				links := 0
				for line := range strings.SplitSeq(text, "\n") {
					pos := strings.Index(line, "https://www.google.com/maps/dir/")
					if pos < 0 {
						continue
					}
					link, err := url.Parse(line[pos:])
					if err != nil {
						t.Fatal(err)
					}
					q := link.Query()
					links++
					if links > 1 && q.Get("origin") != previous {
						t.Fatalf("disconnected leg: %s", line)
					}
					if waypoints := q.Get("waypoints"); waypoints != "" {
						stops := strings.Split(waypoints, "|")
						if len(stops) > 3 {
							t.Fatalf("unsupported %d waypoints: %s", len(stops), line)
						}
						visited = append(visited, stops...)
					}
					previous = q.Get("destination")
					visited = append(visited, previous)
				}
				if !reflect.DeepEqual(visited, expected) {
					t.Fatalf("navigation stops=%v, want %v", visited, expected)
				}
				if count == 1 && links != 1 {
					t.Fatalf("short route links=%d", links)
				}
			})
		}
	}
}

func TestSavedMobileHandoffSurvivesSessionConsumptionAndRosterChanges(t *testing.T) {
	for _, mode := range []models.RouteMode{models.RouteModePickup, models.RouteModeDropoff} {
		t.Run(string(mode), func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			h.PlanDraft = plandraft.NewStore()
			t.Cleanup(h.PlanDraft.Close)
			ctx := context.Background()
			rider, err := store.Participants().Create(ctx, &models.Participant{Name: "Original Rider", Address: "Original Home", Lat: 40.1, Lng: -73})
			if err != nil {
				t.Fatal(err)
			}
			driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Original Driver", Address: "Driver Home", Lat: 40.2, Lng: -73, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			// Empty unused routes must not shift the handoff-to-saved-route association.
			route := models.CalculatedRoute{Driver: driver, EffectiveCapacity: 4, Mode: mode, RouteDurationSecs: 1200, Stops: []models.RouteStop{{Participant: rider, CumulativeDurationSecs: 300}}}
			snapshot := h.RouteSession.Create(routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 99, Name: "Unused", VehicleCapacity: 4}}, route}, ActivityLocation: &models.ActivityLocation{Name: "Grace Center", Address: "1 Grace Way", Lat: 40, Lng: -73}, RouteTime: "18:30", Mode: mode})
			id := h.PlanDraft.NewID()
			h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = snapshot.ID })
			page := mobileRoutePage(t, h, id)
			driverText := handoffTextarea(t, page, "driver-copy-1")
			parentText := handoffTextarea(t, page, "parent-copy-1")
			if !strings.Contains(driverText, "Maps:") || !strings.Contains(driverText, "Grace Center") {
				t.Fatal("live handoff setup missing route details")
			}
			response := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"session_id": {snapshot.ID}, "event_date": {"2026-09-08"}}, h.HandleMobileSave)
			target := response.Header().Get("Location")
			if !strings.HasPrefix(target, "/m/history/") {
				t.Fatalf("save: %d %s", response.Code, target)
			}
			if _, ok := h.RouteSession.Snapshot(snapshot.ID); ok {
				t.Fatal("saved live session not consumed")
			}
			rider.Name = "Edited Rider"
			rider.Address = "Different Home"
			if _, err := store.Participants().Update(ctx, rider); err != nil {
				t.Fatal(err)
			}
			if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
				t.Fatal(err)
			}
			// A fresh handler proves history doesn't depend on the old in-memory plan.
			history := &Handler{DB: store, Renderer: loadEmbeddedTemplates(t)}
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			saved := httptest.NewRecorder()
			history.HandleMobileHistoryDetail(saved, req)
			if saved.Code != 200 {
				t.Fatalf("history: %d %s", saved.Code, saved.Body.String())
			}
			if got := handoffTextarea(t, saved.Body.String(), "saved-driver-copy-0"); got != driverText {
				t.Fatalf("driver handoff changed after save\nwant:%s\ngot:%s", driverText, got)
			}
			if got := handoffTextarea(t, saved.Body.String(), "saved-parent-copy-0"); got != parentText {
				t.Fatalf("parent handoff changed after save\nwant:%s\ngot:%s", parentText, got)
			}
		})
	}
}
