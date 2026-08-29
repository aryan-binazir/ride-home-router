package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"slices"
	"strconv"
	"strings"
	"time"
)

const mobileDraftCookie = "rhr_mobile_draft"

type mobileBaseView struct {
	Title     string
	ActiveTab string
	Error     string
}

func newMobileBase(title, activeTab, message string) mobileBaseView {
	return mobileBaseView{Title: title, ActiveTab: activeTab, Error: message}
}

type mobilePlanView struct {
	mobileBaseView
	Draft        plandraft.Draft
	Location     *models.ActivityLocation
	Participants []models.Participant
	Drivers      []models.Driver
	Vehicles     map[int64]*models.OrganizationVehicle
	LastEvent    *EventWithSummary
	SeatCount    int
}

type mobileLocationView struct {
	mobileBaseView
	Locations  []models.ActivityLocation
	SelectedID int64
}

type mobileRidersView struct {
	mobileBaseView
	Participants      []models.Participant
	Selected          map[int64]bool
	Labels            []models.Label
	LabelIDs          map[int64][]int64
	Search            string
	LabelID           int64
	HiddenSelectedIDs []int64
}

type mobileDriversView struct {
	mobileBaseView
	Drivers           []models.Driver
	Selected          map[int64]bool
	Vehicles          []models.OrganizationVehicle
	Assignments       map[int64]int64
	SelectedSeats     int
	Labels            []models.Label
	LabelIDs          map[int64][]int64
	Search            string
	LabelID           int64
	HiddenSelectedIDs []int64
}

type mobileWhenView struct {
	mobileBaseView
	RouteTime string
	Mode      string
}

type mobileRoute struct {
	Index      int
	Route      models.CalculatedRoute
	DriverText string
	ParentText string
	ETAs       []string
}

type mobileRoutesView struct {
	mobileBaseView
	Snapshot routesession.Snapshot
	Routes   []mobileRoute
}

type mobilePeopleView struct {
	mobileBaseView
	Participants      []models.Participant
	Drivers           []models.Driver
	Labels            []models.Label
	ParticipantLabels map[int64][]int64
	DriverLabels      map[int64][]int64
}

type mobilePersonFormView struct {
	mobileBaseView
	Kind     string
	Action   string
	Person   any
	Labels   []models.Label
	Selected map[int64]bool
}

type mobilePlacesView struct {
	mobileBaseView
	Locations []models.ActivityLocation
	Vans      []models.OrganizationVehicle
}

type mobilePlaceFormView struct {
	mobileBaseView
	Kind   string
	Action string
	Place  any
}

type mobileHistoryView struct {
	mobileBaseView
	Groups   []mobileHistoryGroup
	UseMiles bool
}

type mobileHistoryGroup struct {
	Label  string
	Events []EventWithSummary
}

type mobileHistoryDetailView struct {
	mobileBaseView
	Event    *models.Event
	Routes   []mobileSavedRoute
	Summary  *models.EventSummary
	UseMiles bool
}

type mobileSavedRoute struct {
	Route      models.EventRoute
	DriverText string
	ParentText string
}

func (h *Handler) mobileDraft(w http.ResponseWriter, r *http.Request) (string, plandraft.Draft) {
	if h.PlanDraft == nil {
		h.PlanDraft = plandraft.NewStore()
	}
	if cookie, err := r.Cookie(mobileDraftCookie); err == nil && cookie.Value != "" {
		return cookie.Value, h.PlanDraft.Get(cookie.Value)
	}
	id := h.PlanDraft.NewID()
	//nolint:gosec // Local HTTP is a supported deployment; HttpOnly and SameSite protect this opaque random identifier.
	http.SetCookie(w, &http.Cookie{Name: mobileDraftCookie, Value: id, Path: "/", Secure: r.TLS != nil, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int((8 * time.Hour).Seconds())})
	return id, h.PlanDraft.Get(id)
}

func mobileSelected(ids []int64) map[int64]bool {
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return selected
}

func parseMobileIDs(values []string) []int64 {
	seen := make(map[int64]bool, len(values))
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil && id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func mobileID(path, prefix, suffix string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	value = strings.Trim(value, "/")
	if value == "" || strings.Contains(value, "/") {
		return 0, errors.New("invalid id")
	}
	return strconv.ParseInt(value, 10, 64)
}

func (h *Handler) HandleMobilePlan(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	view := mobilePlanView{mobileBaseView: newMobileBase("Plan", "plan", r.URL.Query().Get("error")), Draft: draft, Vehicles: map[int64]*models.OrganizationVehicle{}}
	if draft.LocationID != 0 {
		view.Location, _ = h.DB.ActivityLocations().GetByID(r.Context(), draft.LocationID)
	}
	view.Participants, _ = h.DB.Participants().GetByIDs(r.Context(), draft.ParticipantIDs)
	view.Drivers, _ = h.DB.Drivers().GetByIDs(r.Context(), draft.DriverIDs)
	for _, driver := range view.Drivers {
		view.SeatCount += driver.VehicleCapacity
	}
	if len(draft.DriverVehicleIDs) > 0 {
		ids := make([]int64, 0, len(draft.DriverVehicleIDs))
		for _, id := range draft.DriverVehicleIDs {
			ids = append(ids, id)
		}
		vehicles, _ := h.DB.OrganizationVehicles().GetByIDs(r.Context(), ids)
		byID := make(map[int64]*models.OrganizationVehicle, len(vehicles))
		for i := range vehicles {
			byID[vehicles[i].ID] = &vehicles[i]
		}
		for driverID, vehicleID := range draft.DriverVehicleIDs {
			if vehicle := byID[vehicleID]; vehicle != nil {
				view.Vehicles[driverID] = vehicle
				for _, driver := range view.Drivers {
					if driver.ID == driverID {
						view.SeatCount += vehicle.Capacity - driver.VehicleCapacity
						break
					}
				}
			}
		}
	}
	if events, err := h.buildEventListView(r.Context(), 1, 0); err == nil && len(events.Events) > 0 {
		view.LastEvent = &events.Events[0]
	}
	h.renderTemplate(w, "mobile/plan.html", view)
}

func (h *Handler) HandleMobileLocation(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/location", "Invalid selection")
			return
		}
		locationID, _ := strconv.ParseInt(r.FormValue("location_id"), 10, 64)
		h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.LocationID = locationID; d.RouteSessionID = "" })
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderTemplate(w, "mobile/location.html", mobileLocationView{mobileBaseView: newMobileBase("Location", "plan", ""), Locations: locations, SelectedID: draft.LocationID})
}

func (h *Handler) HandleMobileRiders(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/riders", "Invalid selection")
			return
		}
		ids := parseMobileIDs(r.Form["participant_ids"])
		h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.ParticipantIDs = ids; d.RouteSessionID = "" })
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	labelID, _ := strconv.ParseInt(r.URL.Query().Get("label"), 10, 64)
	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	labels, _ := h.DB.Labels().List(r.Context())
	labelIDs, _ := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	participants = filterByLabel(participants, func(p models.Participant) int64 { return p.ID }, labelIDs, labelID)
	displayed := make(map[int64]bool, len(participants))
	for _, participant := range participants {
		displayed[participant.ID] = true
	}
	hidden := make([]int64, 0)
	for _, selectedID := range draft.ParticipantIDs {
		if !displayed[selectedID] {
			hidden = append(hidden, selectedID)
		}
	}
	h.renderTemplate(w, "mobile/riders.html", mobileRidersView{mobileBaseView: newMobileBase("Riders", "plan", ""), Participants: participants, Selected: mobileSelected(draft.ParticipantIDs), Labels: labels, LabelIDs: labelIDs, Search: search, LabelID: labelID, HiddenSelectedIDs: hidden})
}

func (h *Handler) HandleMobileDrivers(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/drivers", "Invalid selection")
			return
		}
		driverIDs := parseMobileIDs(r.Form["driver_ids"])
		assignments := make(map[int64]int64)
		for _, driverID := range driverIDs {
			vehicleID, _ := strconv.ParseInt(r.FormValue(fmt.Sprintf("vehicle_%d", driverID)), 10, 64)
			if vehicleID > 0 {
				assignments[driverID] = vehicleID
			}
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) {
			d.DriverIDs = driverIDs
			d.DriverVehicleIDs = assignments
			d.RouteSessionID = ""
		})
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	labelID, _ := strconv.ParseInt(r.URL.Query().Get("label"), 10, 64)
	drivers, err := h.DB.Drivers().List(r.Context(), search)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	vehicles, _ := h.DB.OrganizationVehicles().List(r.Context())
	labels, _ := h.DB.Labels().List(r.Context())
	labelIDs, _ := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	drivers = filterByLabel(drivers, func(d models.Driver) int64 { return d.ID }, labelIDs, labelID)
	displayed := make(map[int64]bool, len(drivers))
	for _, driver := range drivers {
		displayed[driver.ID] = true
	}
	hidden := make([]int64, 0)
	for _, selectedID := range draft.DriverIDs {
		if !displayed[selectedID] {
			hidden = append(hidden, selectedID)
		}
	}
	view := mobileDriversView{mobileBaseView: newMobileBase("Drivers", "plan", ""), Drivers: drivers, Selected: mobileSelected(draft.DriverIDs), Vehicles: vehicles, Assignments: draft.DriverVehicleIDs, Labels: labels, LabelIDs: labelIDs, Search: search, LabelID: labelID, HiddenSelectedIDs: hidden}
	selectedDrivers, _ := h.DB.Drivers().GetByIDs(r.Context(), draft.DriverIDs)
	for _, driver := range selectedDrivers {
		if vehicleID := draft.DriverVehicleIDs[driver.ID]; vehicleID == 0 {
			view.SelectedSeats += driver.VehicleCapacity
		} else {
			for _, vehicle := range vehicles {
				if vehicle.ID == vehicleID {
					view.SelectedSeats += vehicle.Capacity
				}
			}
		}
	}
	h.renderTemplate(w, "mobile/drivers.html", view)
}

func (h *Handler) HandleMobileWhen(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", "Invalid time")
			return
		}
		routeTime, err := parseRouteTime(r.FormValue("route_time"))
		if err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", err.Error())
			return
		}
		mode, err := normalizeRouteMode(r.FormValue("mode"))
		if err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", err.Error())
			return
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteTime = routeTime; d.Mode = string(mode); d.RouteSessionID = "" })
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	h.renderTemplate(w, "mobile/when.html", mobileWhenView{mobileBaseView: newMobileBase("When", "plan", r.URL.Query().Get("error")), RouteTime: draft.RouteTime, Mode: draft.Mode})
}

func (h *Handler) HandleMobileCalculate(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	if draft.LocationID == 0 || len(draft.ParticipantIDs) == 0 || len(draft.DriverIDs) == 0 {
		h.mobileRedirectError(w, r, "/m", "Choose a location, riders, and drivers first")
		return
	}
	mode, err := models.ParseRouteMode(draft.Mode)
	if err != nil {
		h.mobileRedirectError(w, r, "/m", "Choose a valid route mode")
		return
	}
	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(r.Context(), routeCalculationInput{ParticipantIDs: draft.ParticipantIDs, DriverIDs: draft.DriverIDs, ActivityLocationID: draft.LocationID, RouteTime: draft.RouteTime, Mode: mode, OrgVehicleAssignments: draft.DriverVehicleIDs})
	if outcome.Kind != routeCalculationSuccess {
		message := "Could not calculate routes"
		if outcome.Err != nil {
			message = routeCalculationValidationMessage(outcome.Err)
		}
		if outcome.Shortage != nil {
			message = outcome.Shortage.RoutingError.Reason
		}
		h.mobileRedirectError(w, r, "/m", message)
		return
	}
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = outcome.Session.ID })
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileRoutes(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	if draft.RouteSessionID == "" {
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	snapshot, ok := h.RouteSession.Snapshot(draft.RouteSessionID)
	if !ok {
		h.mobileRedirectError(w, r, "/m", "That route plan expired. Calculate it again.")
		return
	}
	view := mobileRoutesView{mobileBaseView: newMobileBase("Routes", "plan", r.URL.Query().Get("error")), Snapshot: snapshot}
	for index, route := range snapshot.Routes {
		view.Routes = append(view.Routes, mobileRoute{Index: index, Route: route, DriverText: formatMobileHandoff(snapshot, route, false), ParentText: formatMobileHandoff(snapshot, route, true), ETAs: mobileETAs(snapshot, route)})
	}
	h.renderTemplate(w, "mobile/routes.html", view)
}

func (h *Handler) HandleMobileMove(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	_ = r.ParseForm()
	participantID, _ := strconv.ParseInt(r.FormValue("participant_id"), 10, 64)
	from, _ := strconv.Atoi(r.FormValue("from_route_index"))
	to, _ := strconv.Atoi(r.FormValue("to_route_index"))
	if _, err := h.RouteSession.ApplyMoves(r.Context(), draft.RouteSessionID, []routesession.Move{{ParticipantID: participantID, FromRouteIndex: from, ToRouteIndex: to, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{RequireClaimedSource: true}); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", err.Error())
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSwap(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	_ = r.ParseForm()
	first, _ := strconv.Atoi(r.FormValue("route_index_1"))
	second, _ := strconv.Atoi(r.FormValue("route_index_2"))
	if _, err := h.RouteSession.SwapDrivers(r.Context(), draft.RouteSessionID, first, second); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", err.Error())
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileReset(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	if _, err := h.RouteSession.Reset(draft.RouteSessionID); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", err.Error())
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileAddDriver(w http.ResponseWriter, r *http.Request) {
	_, draft := h.mobileDraft(w, r)
	_ = r.ParseForm()
	driverID, _ := strconv.ParseInt(r.FormValue("driver_id"), 10, 64)
	if _, err := h.RouteSession.AddDriver(r.Context(), draft.RouteSessionID, driverID); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", err.Error())
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSave(w http.ResponseWriter, r *http.Request) {
	id, draft := h.mobileDraft(w, r)
	_ = r.ParseForm()
	date := r.FormValue("event_date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	var created *models.Event
	err := h.RouteSession.Commit(r.Context(), draft.RouteSessionID, func(ctx context.Context, session routesession.CommitSnapshot) error {
		var err error
		created, _, err = h.persistEvent(ctx, date, strings.TrimSpace(r.FormValue("notes")), session.RoutingResult())
		return err
	})
	if err != nil {
		h.mobileRedirectError(w, r, "/m/routes", err.Error())
		return
	}
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = "" })
	http.Redirect(w, r, fmt.Sprintf("/m/history/%d", created.ID), http.StatusSeeOther)
}

func (h *Handler) HandleMobilePeople(w http.ResponseWriter, r *http.Request) {
	participants, err := h.DB.Participants().List(r.Context(), strings.TrimSpace(r.URL.Query().Get("search")))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	drivers, err := h.DB.Drivers().List(r.Context(), strings.TrimSpace(r.URL.Query().Get("search")))
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	labels, _ := h.DB.Labels().List(r.Context())
	participantLabels, _ := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	driverLabels, _ := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	h.renderTemplate(w, "mobile/people.html", mobilePeopleView{mobileBaseView: newMobileBase("People", "people", ""), Participants: participants, Drivers: drivers, Labels: labels, ParticipantLabels: participantLabels, DriverLabels: driverLabels})
}

func (h *Handler) HandleMobileParticipantForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePersonForm(w, r, "participant")
}

func (h *Handler) HandleMobileDriverForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePersonForm(w, r, "driver")
}

func (h *Handler) mobilePersonForm(w http.ResponseWriter, r *http.Request, kind string) {
	prefix := "/m/people/" + kind + "s/"
	isNew := strings.HasSuffix(r.URL.Path, "/new")
	var id int64
	var err error
	if !isNew {
		id, err = mobileID(r.URL.Path, prefix, "/edit")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	if r.Method == http.MethodPost {
		if err := h.saveMobilePerson(r, kind, id); err != nil {
			h.mobileRedirectError(w, r, r.URL.Path, err.Error())
			return
		}
		http.Redirect(w, r, "/m/people", http.StatusSeeOther)
		return
	}
	labels, _ := h.DB.Labels().List(r.Context())
	selected := map[int64]bool{}
	var person any
	if !isNew && kind == "participant" {
		person, err = h.DB.Participants().GetByID(r.Context(), id)
		if err == nil {
			ids, _ := h.DB.Labels().ListLabelsForParticipant(r.Context(), id)
			for _, label := range ids {
				selected[label.ID] = true
			}
		}
	}
	if !isNew && kind == "driver" {
		person, err = h.DB.Drivers().GetByID(r.Context(), id)
		if err == nil {
			ids, _ := h.DB.Labels().ListLabelsForDriver(r.Context(), id)
			for _, label := range ids {
				selected[label.ID] = true
			}
		}
	}
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	title := "Add participant"
	if kind == "driver" {
		title = "Add driver"
	}
	if !isNew {
		title = strings.Replace(title, "Add", "Edit", 1)
	}
	h.renderTemplate(w, "mobile/person_form.html", mobilePersonFormView{mobileBaseView: newMobileBase(title, "people", r.URL.Query().Get("error")), Kind: kind, Action: r.URL.Path, Person: person, Labels: labels, Selected: selected})
}

func (h *Handler) saveMobilePerson(r *http.Request, kind string, id int64) error {
	if err := r.ParseForm(); err != nil {
		return errors.New("invalid form")
	}
	name, address, addressName := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("address")), strings.TrimSpace(r.FormValue("address_name"))
	if name == "" || address == "" {
		return errors.New(messageNameAndAddressRequired)
	}
	labels := parseMobileIDs(r.Form["label_ids"])
	if err := h.validateLabelIDs(r.Context(), labels); err != nil {
		return errors.New(messageInvalidLabelSelection)
	}
	if kind == "participant" {
		participant := &models.Participant{ID: id, Name: name, Address: address, AddressName: addressName}
		if id > 0 {
			existing, err := h.DB.Participants().GetByID(r.Context(), id)
			if err != nil {
				return err
			}
			participant.Lat, participant.Lng = existing.Lat, existing.Lng
			if existing.Address != address {
				if err := h.geocodeMobile(r.Context(), address, &participant.Lat, &participant.Lng); err != nil {
					return err
				}
			}
			_, err = h.DB.Participants().UpdateWithLabels(r.Context(), participant, labels)
			return err
		}
		if err := h.geocodeMobile(r.Context(), address, &participant.Lat, &participant.Lng); err != nil {
			return err
		}
		_, err := h.DB.Participants().CreateWithLabels(r.Context(), participant, labels)
		return err
	}
	capacity, err := strconv.Atoi(r.FormValue("vehicle_capacity"))
	if err != nil || capacity < models.MinVehicleCapacity || capacity > models.MaxVehicleCapacity {
		return errors.New(messageVehicleCapacityOutOfRange())
	}
	driver := &models.Driver{ID: id, Name: name, Address: address, AddressName: addressName, VehicleCapacity: capacity}
	if id > 0 {
		existing, err := h.DB.Drivers().GetByID(r.Context(), id)
		if err != nil {
			return err
		}
		driver.Lat, driver.Lng = existing.Lat, existing.Lng
		if existing.Address != address {
			if err := h.geocodeMobile(r.Context(), address, &driver.Lat, &driver.Lng); err != nil {
				return err
			}
		}
		_, err = h.DB.Drivers().UpdateWithLabels(r.Context(), driver, labels)
		return err
	}
	if err := h.geocodeMobile(r.Context(), address, &driver.Lat, &driver.Lng); err != nil {
		return err
	}
	_, err = h.DB.Drivers().CreateWithLabels(r.Context(), driver, labels)
	return err
}

func (h *Handler) geocodeMobile(ctx context.Context, address string, lat, lng *float64) error {
	result, err := h.Geocoder.GeocodeWithRetry(ctx, address, 3)
	if err != nil {
		return err
	}
	*lat, *lng = result.Coords.Lat, result.Coords.Lng
	return nil
}

func (h *Handler) HandleMobilePlaces(w http.ResponseWriter, r *http.Request) {
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	vans, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderTemplate(w, "mobile/places.html", mobilePlacesView{mobileBaseView: newMobileBase("Places", "places", ""), Locations: locations, Vans: vans})
}

func (h *Handler) HandleMobileLocationForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePlaceForm(w, r, "location")
}

func (h *Handler) HandleMobileVanForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePlaceForm(w, r, "van")
}

func (h *Handler) mobilePlaceForm(w http.ResponseWriter, r *http.Request, kind string) {
	prefix := "/m/places/"
	if kind == "location" {
		prefix += "locations/"
	} else {
		prefix += "vans/"
	}
	isNew := strings.HasSuffix(r.URL.Path, "/new")
	var id int64
	var err error
	if !isNew {
		id, err = mobileID(r.URL.Path, prefix, "/edit")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	if r.Method == http.MethodPost {
		if err := h.saveMobilePlace(r, kind, id); err != nil {
			h.mobileRedirectError(w, r, r.URL.Path, err.Error())
			return
		}
		http.Redirect(w, r, "/m/places", http.StatusSeeOther)
		return
	}
	var place any
	if !isNew && kind == "location" {
		place, err = h.DB.ActivityLocations().GetByID(r.Context(), id)
	}
	if !isNew && kind == "van" {
		place, err = h.DB.OrganizationVehicles().GetByID(r.Context(), id)
	}
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	title := "Add " + kind
	if !isNew {
		title = "Edit " + kind
	}
	h.renderTemplate(w, "mobile/place_form.html", mobilePlaceFormView{mobileBaseView: newMobileBase(title, "places", r.URL.Query().Get("error")), Kind: kind, Action: r.URL.Path, Place: place})
}

func (h *Handler) saveMobilePlace(r *http.Request, kind string, id int64) error {
	if err := r.ParseForm(); err != nil {
		return errors.New("invalid form")
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return errors.New(strings.ToLower(messageNameRequired))
	}
	if kind == "van" {
		capacity, err := strconv.Atoi(r.FormValue("capacity"))
		if err != nil || capacity < 1 {
			return errors.New(strings.ToLower(messageOrganizationVehicleCapacityMustBeAtLeastOne))
		}
		vehicle := &models.OrganizationVehicle{ID: id, Name: name, Capacity: capacity}
		if id == 0 {
			_, err = h.DB.OrganizationVehicles().Create(r.Context(), vehicle)
		} else {
			_, err = h.DB.OrganizationVehicles().Update(r.Context(), vehicle)
		}
		return err
	}
	address := strings.TrimSpace(r.FormValue("address"))
	if address == "" {
		return errors.New(strings.ToLower(messageAddressRequired))
	}
	location := &models.ActivityLocation{ID: id, Name: name, Address: address}
	if id > 0 {
		existing, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
		if err != nil {
			return err
		}
		location.Lat, location.Lng = existing.Lat, existing.Lng
		if existing.Address != address {
			if err := h.geocodeMobile(r.Context(), address, &location.Lat, &location.Lng); err != nil {
				return err
			}
		}
		_, err = h.DB.ActivityLocations().Update(r.Context(), location)
		return err
	}
	if err := h.geocodeMobile(r.Context(), address, &location.Lat, &location.Lng); err != nil {
		return err
	}
	_, err := h.DB.ActivityLocations().Create(r.Context(), location)
	return err
}

func (h *Handler) HandleMobileHistory(w http.ResponseWriter, r *http.Request) {
	view, err := h.buildEventListView(r.Context(), 100, 0)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	groups := make([]mobileHistoryGroup, 0)
	for _, event := range view.Events {
		label := event.EventDate.Format("January 2006")
		if len(groups) == 0 || groups[len(groups)-1].Label != label {
			groups = append(groups, mobileHistoryGroup{Label: label})
		}
		groups[len(groups)-1].Events = append(groups[len(groups)-1].Events, event)
	}
	h.renderTemplate(w, "mobile/history.html", mobileHistoryView{mobileBaseView: newMobileBase("History", "history", ""), Groups: groups, UseMiles: view.UseMiles})
}

func (h *Handler) HandleMobileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	id, err := mobileID(r.URL.Path, "/m/history/", "")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	event, routes, summary, err := h.DB.Events().GetByID(r.Context(), id)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	settings, _ := h.DB.Settings().Get(r.Context())
	savedRoutes := make([]mobileSavedRoute, 0, len(routes))
	for _, route := range routes {
		savedRoutes = append(savedRoutes, mobileSavedRoute{Route: route, DriverText: formatSavedMobileHandoff(route, false), ParentText: formatSavedMobileHandoff(route, true)})
	}
	h.renderTemplate(w, "mobile/history_detail.html", mobileHistoryDetailView{mobileBaseView: newMobileBase("Saved event", "history", ""), Event: event, Routes: savedRoutes, Summary: summary, UseMiles: settings != nil && settings.UseMiles})
}

func (h *Handler) HandleMobileAddressSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("address"))
	if len(query) < 4 {
		w.WriteHeader(http.StatusOK)
		return
	}
	results, err := h.Geocoder.Search(r.Context(), query, 5)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.renderTemplate(w, "mobile_address_suggestions.html", results)
}

func (h *Handler) HandleMobileDesktopPreference(w http.ResponseWriter, r *http.Request) {
	//nolint:gosec // Local HTTP is a supported deployment; this preference cookie carries no sensitive value.
	http.SetCookie(w, &http.Cookie{Name: "prefer_desktop", Value: "1", Path: "/", Secure: r.TLS != nil, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) mobileRedirectError(w http.ResponseWriter, r *http.Request, path, message string) {
	if !strings.HasPrefix(path, "/m") {
		path = "/m"
	}
	target, _ := url.Parse(path)
	query := target.Query()
	query.Set("error", message)
	target.RawQuery = query.Encode()
	//nolint:gosec // The prefix check above restricts redirects to the same-origin mobile application.
	http.Redirect(w, r, target.String(), http.StatusSeeOther)
}

func mobileETAs(snapshot routesession.Snapshot, route models.CalculatedRoute) []string {
	base, err := time.Parse("15:04", snapshot.RouteTime)
	if err != nil {
		return make([]string, len(route.Stops))
	}
	values := make([]string, len(route.Stops))
	for i, stop := range route.Stops {
		seconds := stop.CumulativeDurationSecs
		if snapshot.Mode == models.RouteModePickup {
			seconds -= route.RouteDurationSecs
		} else {
			seconds += 120
		}
		values[i] = base.Add(time.Duration(seconds * float64(time.Second))).Format("3:04 PM")
	}
	return values
}

func formatMobileHandoff(snapshot routesession.Snapshot, route models.CalculatedRoute, parents bool) string {
	var b strings.Builder
	locationName, locationAddress := "Activity location", ""
	if snapshot.ActivityLocation != nil {
		locationName, locationAddress = snapshot.ActivityLocation.Name, snapshot.ActivityLocation.Address
	}
	fmt.Fprintf(&b, "Activity Location: %s\n%s\n\n", locationName, locationAddress)
	if route.Driver == nil {
		return b.String()
	}
	fmt.Fprintf(&b, "Driver: %s\n", route.Driver.Name)
	if !parents {
		fmt.Fprintf(&b, "%s\n", displayMobileAddress(route.Driver.AddressName, route.Driver.Address))
	}
	etas := mobileETAs(snapshot, route)
	for i, stop := range route.Stops {
		if stop.Participant == nil {
			continue
		}
		fmt.Fprintf(&b, "%d. ", i+1)
		if i < len(etas) && etas[i] != "" {
			fmt.Fprintf(&b, "%s - ", etas[i])
		}
		b.WriteString(stop.Participant.Name)
		if !parents {
			fmt.Fprintf(&b, " - %s", displayMobileAddress(stop.Participant.AddressName, stop.Participant.Address))
		}
		b.WriteByte('\n')
	}
	if !parents {
		fmt.Fprintf(&b, "\nMaps: %s\n", mobileMapsURL(snapshot, route))
	}
	return b.String()
}

func formatSavedMobileHandoff(route models.EventRoute, parents bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Driver: %s\n", route.DriverName)
	if !parents {
		fmt.Fprintf(&b, "%s\n", displayMobileAddress(route.DriverAddressName, route.DriverAddress))
	}
	for index, stop := range route.Stops {
		fmt.Fprintf(&b, "%d. %s", index+1, stop.ParticipantName)
		if !parents {
			fmt.Fprintf(&b, " - %s", displayMobileAddress(stop.ParticipantAddressName, stop.ParticipantAddress))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func displayMobileAddress(name, address string) string {
	if strings.TrimSpace(name) == "" {
		return address
	}
	return fmt.Sprintf("%s (%s)", name, address)
}

func mobileMapsURL(snapshot routesession.Snapshot, route models.CalculatedRoute) string {
	if snapshot.ActivityLocation == nil || route.Driver == nil || len(route.Stops) == 0 {
		return ""
	}
	location := func(lat, lng float64, address string) string {
		if lat != 0 || lng != 0 {
			return fmt.Sprintf("%g,%g", lat, lng)
		}
		return address
	}
	activity := location(snapshot.ActivityLocation.Lat, snapshot.ActivityLocation.Lng, snapshot.ActivityLocation.Address)
	driver := location(route.Driver.Lat, route.Driver.Lng, route.Driver.Address)
	stops := make([]string, 0, len(route.Stops))
	seen := map[string]bool{}
	for _, stop := range route.Stops {
		if stop.Participant == nil {
			continue
		}
		value := location(stop.Participant.Lat, stop.Participant.Lng, stop.Participant.Address)
		if !seen[value] {
			seen[value] = true
			stops = append(stops, value)
		}
	}
	points := append([]string{activity}, stops...)
	points = append(points, driver)
	if snapshot.Mode == models.RouteModePickup {
		points[0], points[len(points)-1] = points[len(points)-1], points[0]
	}
	query := url.Values{"api": {"1"}, "travelmode": {"driving"}, "dir_action": {"navigate"}, "destination": {points[len(points)-1]}}
	if len(points) > 2 {
		query.Set("waypoints", strings.Join(points[1:len(points)-1], "|"))
	}
	return "https://www.google.com/maps/dir/?" + query.Encode()
}

func filterByLabel[T any](items []T, ids func(T) int64, memberships map[int64][]int64, labelID int64) []T {
	if labelID == 0 {
		return items
	}
	result := make([]T, 0, len(items))
	for _, item := range items {
		if slices.Contains(memberships[ids(item)], labelID) {
			result = append(result, item)
		}
	}
	return result
}
