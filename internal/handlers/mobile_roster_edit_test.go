package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
)

func TestRosterEditLookupAndValidationPrecedence(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		for _, mobile := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/json", true: "/mobile"}[mobile], func(t *testing.T) {
				h, _ := newTestManagementHandler(t)
				if mobile {
					invoke := h.HandleMobileParticipantForm
					if kind == "driver" {
						invoke = h.HandleMobileDriverForm
					}
					rr := postMobileForm(t, nil, "/m/people/"+kind+"s/999999/edit", url.Values{}, invoke)
					if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), messageNameAndAddressRequired) {
						t.Fatalf("response = %d %s", rr.Code, rr.Body.String())
					}
					valid := url.Values{"name": {"Person"}, "address": {"Address"}, "vehicle_capacity": {"4"}}
					rr = postMobileForm(t, nil, "/m/people/"+kind+"s/999999/edit", valid, invoke)
					if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), mobilePersonNotFoundMessage(kind)) || !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
						t.Fatalf("valid missing edit = %d %s", rr.Code, rr.Body.String())
					}
				} else {
					for _, body := range []string{"{", `{"name":"","address":""}`} {
						req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/"+kind+"s/999999", strings.NewReader(body))
						req.Header.Set("Content-Type", "application/json")
						rr := httptest.NewRecorder()
						if kind == "driver" {
							h.HandleUpdateDriver(rr, req)
						} else {
							h.HandleUpdateParticipant(rr, req)
						}
						message := messageParticipantNotFound
						if kind == "driver" {
							message = messageDriverNotFound
						}
						want := `{"error":{"code":"NOT_FOUND","message":"` + message + `"}}` + "\n"
						if rr.Code != http.StatusNotFound || rr.Body.String() != want {
							t.Fatalf("response = %d %q, want 404 %q", rr.Code, rr.Body.String(), want)
						}
					}
				}
			})
		}
	}
}

func TestRosterEditDriverCapacityAndLabelPrecedence(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	rr := postMobileForm(t, nil, "/m/people/drivers/new", url.Values{"name": {"Driver"}, "address": {"Address"}, "vehicle_capacity": {"999"}, "label_ids": {"999999"}}, h.HandleMobileDriverForm)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), messageInvalidLabelSelection) {
		t.Fatalf("mobile = %d %s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/drivers", strings.NewReader(`{"name":"Driver","address":"Address","vehicle_capacity":999,"label_ids":[999999]}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.HandleCreateDriver(rr, req)
	if rr.Code != http.StatusBadRequest || rr.Body.String() != `{"error":{"code":"VALIDATION_ERROR","message":"`+messageVehicleCapacityOutOfRange()+`"}}`+"\n" {
		t.Fatalf("json = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRosterEditGeocodeErrorPresentation(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		for _, surface := range []string{"json", "htmx", "mobile"} {
			for _, updating := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/update=%t", kind, surface, updating), func(t *testing.T) {
					h, store := newTestManagementHandler(t)
					var id int64
					if updating {
						if kind == "participant" {
							id = createValidationParticipant(t, h)
						} else {
							id = createValidationDriver(t, h)
						}
					}
					h.Geocoder = stubGeocoder{err: errors.New("provider detail sentinel")}
					rr := rosterEditHTTPRequest(t, h, kind, surface, id, url.Values{"name": {"Must not persist"}, "address": {"Changed address"}, "vehicle_capacity": {"4"}})
					switch surface {
					case "json":
						if rr.Code != http.StatusUnprocessableEntity || rr.Body.String() != `{"error":{"code":"GEOCODING_FAILED","message":"`+messageAddressLookupUnavailable+`"}}`+"\n" {
							t.Fatalf("json = %d %s", rr.Code, rr.Body.String())
						}
					case "htmx":
						if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(rr.Body.String(), messageAddressLookupUnavailable) || !strings.Contains(rr.Header().Get("HX-Trigger"), messageAddressLookupUnavailable) {
							t.Fatalf("htmx = %d %s", rr.Code, rr.Body.String())
						}
					case "mobile":
						if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), messageAddressLookupUnavailable) || strings.Contains(rr.Body.String(), "provider detail sentinel") || !strings.Contains(rr.Body.String(), "Must not persist") {
							t.Fatalf("mobile = %d %s", rr.Code, rr.Body.String())
						}
					}
					participants, err := store.Participants().List(context.Background(), "")
					if err != nil {
						t.Fatal(err)
					}
					drivers, err := store.Drivers().List(context.Background(), "")
					if err != nil {
						t.Fatal(err)
					}
					wantParticipants, wantDrivers := 0, 0
					if updating {
						if kind == "participant" {
							wantParticipants = 1
						} else {
							wantDrivers = 1
						}
					}
					if len(participants) != wantParticipants || len(drivers) != wantDrivers {
						t.Fatalf("roster = %v, %v", participants, drivers)
					}
					if updating && kind == "participant" && (participants[0].Name != "Participant" || participants[0].Address != "1 Rider Way" || participants[0].Lat != 40 || participants[0].Lng != -73) {
						t.Fatalf("participant changed = %#v", participants[0])
					}
					if updating && kind == "driver" && (drivers[0].Name != "Driver" || drivers[0].Address != "1 Driver Way" || drivers[0].Lat != 40 || drivers[0].Lng != -73) {
						t.Fatalf("driver changed = %#v", drivers[0])
					}
				})
			}
		}
	}
}

func TestRosterEditHTTPNormalizationAndLabels(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		for _, surface := range []string{"json", "htmx", "mobile"} {
			t.Run(kind+"/"+surface, func(t *testing.T) {
				h, store := newTestManagementHandler(t)
				ctx := context.Background()
				label, err := store.Labels().Create(ctx, &models.Label{Name: "Keep"})
				if err != nil {
					t.Fatal(err)
				}
				form := url.Values{"name": {" Person "}, "address": {" Address "}, "address_name": {" Home "}, "vehicle_capacity": {"4"}, "label_ids": {strconv.FormatInt(label.ID, 10)}}
				rr := rosterEditHTTPRequest(t, h, kind, surface, 0, form)
				wantStatus := http.StatusCreated
				if surface == "htmx" {
					wantStatus = http.StatusOK
				}
				if surface == "mobile" {
					wantStatus = http.StatusSeeOther
				}
				if rr.Code != wantStatus {
					t.Fatalf("create = %d %s", rr.Code, rr.Body.String())
				}
				var id int64
				var originalParticipant models.Participant
				var originalDriver models.Driver
				var name, address, addressName string
				if kind == "participant" {
					people, listErr := store.Participants().List(ctx, "")
					if listErr != nil || len(people) != 1 {
						t.Fatalf("people = %v, %v", people, listErr)
					}
					originalParticipant = people[0]
					id, name, address, addressName = people[0].ID, people[0].Name, people[0].Address, people[0].AddressName
				} else {
					people, listErr := store.Drivers().List(ctx, "")
					if listErr != nil || len(people) != 1 {
						t.Fatalf("people = %v, %v", people, listErr)
					}
					originalDriver = people[0]
					id, name, address, addressName = people[0].ID, people[0].Name, people[0].Address, people[0].AddressName
				}
				wantName, wantAddress := " Person ", " Address "
				if surface == "mobile" {
					wantName, wantAddress = "Person", "Address"
				}
				if name != wantName || address != wantAddress || addressName != "Home" {
					t.Fatalf("normalized = %q %q %q", name, address, addressName)
				}
				provider := &rosterTestGeocoder{result: &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 43, Lng: -70}}}
				h.Geocoder = provider
				// Malformed label input cannot clear a membership or persist other edits.
				form.Set("name", "Should not save")
				form.Set("label_ids", "broken")
				rr = rosterEditHTTPRequest(t, h, kind, surface, id, form)
				wantStatus = http.StatusBadRequest
				if surface == "htmx" {
					wantStatus = http.StatusBadRequest
				}
				if rr.Code != wantStatus {
					t.Fatalf("malformed = %d %s", rr.Code, rr.Body.String())
				}
				assertRosterEditLabels(t, h, kind, id, label.ID)
				if kind == "participant" {
					got, getErr := store.Participants().GetByID(ctx, id)
					if getErr != nil || !reflect.DeepEqual(got, &originalParticipant) {
						t.Fatalf("rejected edit changed participant = %#v, want %#v, err=%v", got, originalParticipant, getErr)
					}
				} else {
					got, getErr := store.Drivers().GetByID(ctx, id)
					if getErr != nil || !reflect.DeepEqual(got, &originalDriver) {
						t.Fatalf("rejected edit changed driver = %#v, want %#v, err=%v", got, originalDriver, getErr)
					}
				}
				// Omitted JSON labels retain membership; forms replace with an empty set.
				form.Set("name", " Updated ")
				form.Del("label_ids")
				rr = rosterEditHTTPRequest(t, h, kind, surface, id, form)
				wantStatus = http.StatusOK
				if surface == "mobile" {
					wantStatus = http.StatusSeeOther
				}
				if rr.Code != wantStatus || len(provider.addresses) != 0 {
					t.Fatalf("unchanged address = %d calls=%v %s", rr.Code, provider.addresses, rr.Body.String())
				}
				if surface == "json" {
					assertRosterEditLabels(t, h, kind, id, label.ID)
				} else {
					assertRosterEditLabels(t, h, kind, id)
				}
				// An explicit empty JSON array also replaces. Every adapter geocodes a changed address.
				form["label_ids"] = []string{}
				form.Set("address", "Changed address")
				rr = rosterEditHTTPRequest(t, h, kind, surface, id, form)
				if rr.Code != wantStatus || len(provider.addresses) != 1 || provider.addresses[0] != "Changed address" {
					t.Fatalf("changed address = %d calls=%v %s", rr.Code, provider.addresses, rr.Body.String())
				}
				assertRosterEditLabels(t, h, kind, id)
				wantName = " Updated "
				if surface == "mobile" {
					wantName = "Updated"
				}
				if kind == "participant" {
					person, getErr := store.Participants().GetByID(ctx, id)
					if getErr != nil || person.Name != wantName || person.Lat != 43 || person.Lng != -70 || person.Address != "Changed address" {
						t.Fatalf("updated = %#v, %v", person, getErr)
					}
				} else {
					person, getErr := store.Drivers().GetByID(ctx, id)
					if getErr != nil || person.Name != wantName || person.Lat != 43 || person.Lng != -70 || person.Address != "Changed address" {
						t.Fatalf("updated = %#v, %v", person, getErr)
					}
				}
			})
		}
	}
}

func rosterEditHTTPRequest(t *testing.T, h *Handler, kind, surface string, id int64, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/" + kind + "s"
	method := http.MethodPost
	invoke := h.HandleCreateParticipant
	if kind == "driver" {
		invoke = h.HandleCreateDriver
	}
	if id > 0 {
		path += "/" + strconv.FormatInt(id, 10)
		method = http.MethodPut
		invoke = h.HandleUpdateParticipant
		if kind == "driver" {
			invoke = h.HandleUpdateDriver
		}
	}
	if surface == "mobile" {
		path = "/m/people/" + kind + "s/new"
		if id > 0 {
			path = "/m/people/" + kind + "s/" + strconv.FormatInt(id, 10) + "/edit"
		}
		invoke = h.HandleMobileParticipantForm
		if kind == "driver" {
			invoke = h.HandleMobileDriverForm
		}
		return postMobileForm(t, nil, path, form, invoke)
	}
	body, contentType := form.Encode(), "application/x-www-form-urlencoded"
	if surface == "json" {
		capacity, _ := strconv.Atoi(form.Get("vehicle_capacity"))
		payload := map[string]any{"name": form.Get("name"), "address": form.Get("address"), "address_name": form.Get("address_name"), "vehicle_capacity": capacity}
		if labels, present := form["label_ids"]; present {
			ids := []int64{}
			for _, label := range labels {
				id, err := strconv.ParseInt(label, 10, 64)
				if err != nil {
					payload["label_ids"] = "broken"
					break
				}
				ids = append(ids, id)
			}
			if payload["label_ids"] == nil {
				payload["label_ids"] = ids
			}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body, contentType = string(encoded), "application/json"
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if surface == "htmx" {
		req.Header.Set("HX-Request", "true")
	}
	rr := httptest.NewRecorder()
	invoke(rr, req)
	return rr
}

func assertRosterEditLabels(t *testing.T, h *Handler, kind string, id int64, wantIDs ...int64) {
	t.Helper()
	var labels []models.Label
	var err error
	if kind == "participant" {
		labels, err = h.DB.Labels().ListLabelsForParticipant(context.Background(), id)
	} else {
		labels, err = h.DB.Labels().ListLabelsForDriver(context.Background(), id)
	}
	if err != nil || len(labels) != len(wantIDs) {
		t.Fatalf("labels = %v, want %v, err=%v", labels, wantIDs, err)
	}
	for i, id := range wantIDs {
		if labels[i].ID != id {
			t.Fatalf("labels = %v, want %v", labels, wantIDs)
		}
	}
}
