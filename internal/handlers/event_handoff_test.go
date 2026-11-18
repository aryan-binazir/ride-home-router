package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func TestEventSaveKeepsHandoffsAfterRosterChanges(t *testing.T) {
	for _, mode := range []models.RouteMode{models.RouteModePickup, models.RouteModeDropoff} {
		t.Run(string(mode), func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			ctx := t.Context()
			rider, err := store.Participants().Create(ctx, &models.Participant{Name: "Original Rider", Address: "Original Home", AddressName: "Apartment", Lat: 40.1, Lng: -73})
			if err != nil {
				t.Fatal(err)
			}
			driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Original Driver", Address: "Driver Home", VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			session := mustCreateRouteSession(t, h.RouteSession, routesession.CreateInput{
				Routes: []models.CalculatedRoute{
					{Driver: &models.Driver{ID: 99, Name: "Unused"}},
					{Driver: driver, Mode: mode, Stops: []models.RouteStop{
						{Participant: rider, CumulativeDurationSecs: 300},
						{Participant: &models.Participant{ID: rider.ID + 1, Name: "Rider Two", Lat: 40.2, Lng: -73}},
					}},
				},
				ActivityLocation: &models.ActivityLocation{Name: "Grace Center", Address: "1 Grace Way"},
				RouteTime:        "18:30",
				Mode:             mode,
			})
			eventID := saveHandoffEvent(t, h, session.ID)
			if _, ok := mustLoadRouteSession(t, h.RouteSession, session.ID); ok {
				t.Fatal("saved live session was not consumed")
			}
			rider.Name = "Edited Rider"
			rider.Address = "Different Home"
			if _, err := store.Participants().Update(ctx, rider); err != nil {
				t.Fatal(err)
			}
			if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
				t.Fatal(err)
			}
			_, routes, _, err := store.Events().GetByID(ctx, eventID)
			if err != nil || len(routes) != 1 {
				t.Fatalf("event routes=%+v err=%v", routes, err)
			}
			header := "Pickup: arrive at Grace Center by 6:30 PM"
			destination := "1 Grace Way"
			if mode == models.RouteModeDropoff {
				header = "Dropoff: leave Grace Center at 6:30 PM"
				destination = "Driver Home"
			}
			text := header + "\n\nActivity Location: Grace Center\n1 Grace Way\n\nDriver: Original Driver\n"
			if want := text + "1. Original Rider\n2. Rider Two\n"; routes[0].ParentHandoff != want {
				t.Fatalf("parent handoff = %q, want %q", routes[0].ParentHandoff, want)
			}
			query := url.Values{"api": {"1"}, "travelmode": {"driving"}, "dir_action": {"navigate"}, "destination": {destination}, "waypoints": {"40.100000,-73.000000|40.200000,-73.000000"}}
			want := text + "Driver Home\n1. Original Rider - Apartment (Original Home)\n2. Rider Two\n\nMaps: https://www.google.com/maps/dir/?" + query.Encode() + "\n"
			if routes[0].DriverHandoff != want {
				t.Fatalf("driver handoff = %q, want %q", routes[0].DriverHandoff, want)
			}
		})
	}
}

func TestSavedHandoffMapsLegsFollowStopOrder(t *testing.T) {
	h, store := newTestManagementHandler(t)
	route := models.CalculatedRoute{
		Driver: &models.Driver{ID: 1, Name: "Driver", Address: "Home", VehicleCapacity: 12},
	}
	var expected []string
	for i := 1; i <= 10; i++ {
		lat := 40 + float64(i)/100
		if i == 2 || i == 4 {
			lat = 40.01
		}
		route.Stops = append(route.Stops, models.RouteStop{Participant: &models.Participant{
			ID: int64(i), Name: fmt.Sprintf("Rider %d", i), Lat: lat, Lng: -73,
		}})
		if i != 2 {
			expected = append(expected, fmt.Sprintf("%.6f,-73.000000", lat))
		}
	}
	expected = append(expected, "Home")
	session := mustCreateRouteSession(t, h.RouteSession, routesession.CreateInput{
		Routes: []models.CalculatedRoute{route}, Mode: models.RouteModeDropoff,
		RouteTime: "18:30", ActivityLocation: &models.ActivityLocation{Address: "Venue"},
	})
	eventID := saveHandoffEvent(t, h, session.ID)
	_, routes, _, err := store.Events().GetByID(t.Context(), eventID)
	if err != nil || len(routes) != 1 {
		t.Fatalf("event routes=%+v err=%v", routes, err)
	}
	var visited []string
	previous := ""
	legs := 0
	for line := range strings.SplitSeq(routes[0].DriverHandoff, "\n") {
		pos := strings.Index(line, "https://www.google.com/maps/dir/")
		if pos < 0 {
			continue
		}
		link, err := url.Parse(line[pos:])
		if err != nil {
			t.Fatal(err)
		}
		legs++
		q := link.Query()
		if !strings.HasPrefix(line, fmt.Sprintf("Maps leg %d of 3: ", legs)) || q.Get("origin") != previous {
			t.Fatalf("incorrect leg label or origin: %s", line)
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
	if legs != 3 || !reflect.DeepEqual(visited, expected) {
		t.Fatalf("navigation legs=%d stops=%v, want 3 legs and %v", legs, visited, expected)
	}
}

func TestSavedHandoffOmitsMapsLinkForUnresolvedLocations(t *testing.T) {
	h, store := newTestManagementHandler(t)
	for _, missing := range []string{"activity", "driver", "rider"} {
		t.Run(missing, func(t *testing.T) {
			location := &models.ActivityLocation{Name: "Activity", Address: "Venue"}
			driver := &models.Driver{ID: 1, Name: "Driver", Address: "Home", VehicleCapacity: 4}
			rider := &models.Participant{ID: 2, Name: "Rider", Address: "Stop"}
			switch missing {
			case "activity":
				location.Address = ""
			case "driver":
				driver.Address = ""
			case "rider":
				rider.Address = ""
			}
			session := mustCreateRouteSession(t, h.RouteSession, routesession.CreateInput{
				Routes:           []models.CalculatedRoute{{Driver: driver, Stops: []models.RouteStop{{Participant: rider}}}},
				ActivityLocation: location, Mode: models.RouteModeDropoff,
			})
			eventID := saveHandoffEvent(t, h, session.ID)
			_, routes, _, err := store.Events().GetByID(t.Context(), eventID)
			if err != nil || len(routes) != 1 {
				t.Fatalf("event routes=%+v err=%v", routes, err)
			}
			text := routes[0].DriverHandoff
			if strings.Contains(text, "Maps") || !strings.Contains(text, "1. Rider") {
				t.Fatalf("unresolved address produced a misleading handoff: %q", text)
			}
		})
	}
}

func saveHandoffEvent(t *testing.T, h *Handler, sessionID string) int64 {
	t.Helper()
	response := postForm(t, "/api/v1/events", url.Values{"session_id": {sessionID}, "event_date": {"2026-09-08"}}, h.HandleCreateEvent)
	var event models.Event
	if response.Code != http.StatusCreated {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&event); err != nil {
		t.Fatal(err)
	}
	return event.ID
}
