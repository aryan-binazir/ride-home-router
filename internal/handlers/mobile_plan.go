package handlers

import (
	"context"
	"net/http"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"strconv"
	"strings"
)

func (h *Handler) HandleMobilePlan(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, notice := h.mobileDraft(w, r)
	var err error
	draft, notice, err = h.pruneMobileDraft(r.Context(), id, draft, notice)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Plan not found")
		return
	}

	base := newMobileBase("Plan", "plan", r.URL.Query().Get("error"))
	base.Notice = notice
	view := mobilePlanView{
		mobileBaseView:   base,
		Draft:            draft,
		RouteTimeDisplay: formatMobileTime(draft.RouteTime),
	}
	if draft.LocationID != 0 {
		view.Location, err = h.DB.ActivityLocations().GetByID(r.Context(), draft.LocationID)
		if h.checkNotFound(err) {
			view.Location = nil
			draft = h.PlanDraft.Update(id, func(d *plandraft.Draft) {
				d.LocationID = 0
				d.RouteSessionID = ""
			})
			view.Draft = draft
			view.Notice = mergeMobileNotice(view.Notice, "An unavailable place was removed from this plan.")
		} else if err != nil {
			h.renderMobileStoreError(w, r, err, "Location not found")
			return
		}
	}
	view.Participants, err = h.DB.Participants().GetByIDs(r.Context(), draft.ParticipantIDs)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Participants not found")
		return
	}
	view.Drivers, err = h.DB.Drivers().GetByIDs(r.Context(), draft.DriverIDs)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Drivers not found")
		return
	}
	for _, driver := range view.Drivers {
		view.SeatCount += driver.VehicleCapacity
	}
	if len(draft.DriverVehicleIDs) > 0 {
		vehicleIDs := make([]int64, 0, len(draft.DriverVehicleIDs))
		for _, vehicleID := range draft.DriverVehicleIDs {
			vehicleIDs = append(vehicleIDs, vehicleID)
		}
		vehicles, listErr := h.DB.OrganizationVehicles().GetByIDs(r.Context(), vehicleIDs)
		if listErr != nil {
			h.renderMobileStoreError(w, r, listErr, "Vans not found")
			return
		}
		byID := make(map[int64]models.OrganizationVehicle, len(vehicles))
		for _, vehicle := range vehicles {
			byID[vehicle.ID] = vehicle
		}
		for _, driver := range view.Drivers {
			if vehicle, ok := byID[draft.DriverVehicleIDs[driver.ID]]; ok {
				view.SeatCount += vehicle.Capacity - driver.VehicleCapacity
			}
		}
	}
	if events, listErr := h.buildEventListView(r.Context(), 1, 0); listErr == nil && len(events.Events) > 0 {
		view.LastEvent = &events.Events[0]
	}
	h.renderTemplate(w, "mobile/plan.html", view)
}

func (h *Handler) HandleMobileLocation(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, _ := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/location", messageMobileInvalidForm)
			return
		}
		locationID, err := strconv.ParseInt(r.FormValue("location_id"), 10, 64)
		if err != nil || locationID <= 0 {
			h.mobileRedirectError(w, r, "/m/plan/location", "Choose a valid location.")
			return
		}
		if _, err := h.DB.ActivityLocations().GetByID(r.Context(), locationID); err != nil {
			if h.checkNotFound(err) {
				h.mobileRedirectError(w, r, "/m/plan/location", "Choose a valid location.")
				return
			}
			h.renderMobileStoreError(w, r, err, "Location not found")
			return
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) {
			d.LocationID = locationID
			d.RouteSessionID = ""
		})
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Locations not found")
		return
	}
	h.renderTemplate(w, "mobile/location.html", mobileLocationView{mobileBaseView: newMobileBase("Location", "plan", ""), Locations: locations, SelectedID: draft.LocationID})
}

func (h *Handler) HandleMobileRiders(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, notice := h.mobileDraft(w, r)
	var err error
	draft, notice, err = h.pruneMobileDraft(r.Context(), id, draft, notice)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Participants not found")
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/riders", messageMobileInvalidForm)
			return
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) {
			d.ParticipantIDs = parseMobileIDs(r.Form["participant_ids"])
			d.RouteSessionID = ""
		})
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}

	selectedIDs := draft.ParticipantIDs
	if h.isHTMX(r) {
		selectedIDs = parseMobileIDs(r.URL.Query()["participant_ids"])
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	labelID, _ := strconv.ParseInt(r.URL.Query().Get("label"), 10, 64)
	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Participants not found")
		return
	}
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Labels not found")
		return
	}
	labelIDs, err := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Labels not found")
		return
	}
	participants = filterByLabel(participants, func(p models.Participant) int64 { return p.ID }, labelIDs, labelID)
	displayed := make(map[int64]bool, len(participants))
	for _, participant := range participants {
		displayed[participant.ID] = true
	}
	hidden := hiddenMobileIDs(selectedIDs, displayed)
	base := newMobileBase("Riders", "plan", "")
	base.Notice = notice
	view := mobileRidersView{
		mobileBaseView:    base,
		Participants:      participants,
		Selected:          mobileSelected(selectedIDs),
		Labels:            labels,
		LabelIDs:          labelIDs,
		Search:            search,
		LabelID:           labelID,
		HiddenSelectedIDs: hidden,
	}
	h.renderTemplate(w, "mobile/riders.html", view)
}

func (h *Handler) HandleMobileDrivers(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, notice := h.mobileDraft(w, r)
	var err error
	draft, notice, err = h.pruneMobileDraft(r.Context(), id, draft, notice)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Drivers not found")
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/drivers", messageMobileInvalidForm)
			return
		}
		driverIDs := parseMobileIDs(r.Form["driver_ids"])
		assignments, err := parseOrgVehicleAssignments(r.Form, driverIDs)
		if err != nil {
			h.mobileRedirectError(w, r, "/m/plan/drivers", mobileVanAssignmentMessage(err))
			return
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) {
			d.DriverIDs = driverIDs
			d.DriverVehicleIDs = assignments
			d.RouteSessionID = ""
		})
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}

	selectedIDs := draft.DriverIDs
	assignments := draft.DriverVehicleIDs
	if h.isHTMX(r) {
		selectedIDs = parseMobileIDs(r.URL.Query()["driver_ids"])
		if parsed, parseErr := parseOrgVehicleAssignments(r.URL.Query(), selectedIDs); parseErr == nil {
			assignments = parsed
		}
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	labelID, _ := strconv.ParseInt(r.URL.Query().Get("label"), 10, 64)
	drivers, err := h.DB.Drivers().List(r.Context(), search)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Drivers not found")
		return
	}
	vehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Vans not found")
		return
	}
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Labels not found")
		return
	}
	labelIDs, err := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Labels not found")
		return
	}
	drivers = filterByLabel(drivers, func(d models.Driver) int64 { return d.ID }, labelIDs, labelID)
	displayed := make(map[int64]bool, len(drivers))
	for _, driver := range drivers {
		displayed[driver.ID] = true
	}
	base := newMobileBase("Drivers", "plan", "")
	base.Notice = notice
	view := mobileDriversView{
		mobileBaseView:    base,
		Drivers:           drivers,
		Selected:          mobileSelected(selectedIDs),
		Vehicles:          vehicles,
		Assignments:       assignments,
		Labels:            labels,
		LabelIDs:          labelIDs,
		Search:            search,
		LabelID:           labelID,
		HiddenSelectedIDs: hiddenMobileIDs(selectedIDs, displayed),
	}
	selectedDrivers, err := h.DB.Drivers().GetByIDs(r.Context(), selectedIDs)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Drivers not found")
		return
	}
	for _, driver := range selectedDrivers {
		view.SelectedSeats += driver.VehicleCapacity
		for _, vehicle := range vehicles {
			if vehicle.ID == assignments[driver.ID] {
				view.SelectedSeats += vehicle.Capacity - driver.VehicleCapacity
				break
			}
		}
	}
	h.renderTemplate(w, "mobile/drivers.html", view)
}

func (h *Handler) HandleMobileWhen(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, _ := h.mobileDraft(w, r)
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", messageMobileInvalidForm)
			return
		}
		routeTime, err := parseRouteTime(r.FormValue("route_time"))
		if err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", messageChooseValidRouteTime)
			return
		}
		mode, err := normalizeRouteMode(r.FormValue("mode"))
		if err != nil {
			h.mobileRedirectError(w, r, "/m/plan/when", messageInvalidRouteMode)
			return
		}
		h.PlanDraft.Update(id, func(d *plandraft.Draft) {
			d.RouteTime = routeTime
			d.Mode = string(mode)
			d.RouteSessionID = ""
		})
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	h.renderTemplate(w, "mobile/when.html", mobileWhenView{mobileBaseView: newMobileBase("When", "plan", r.URL.Query().Get("error")), RouteTime: draft.RouteTime, Mode: draft.Mode})
}

func (h *Handler) HandleMobileCalculate(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, notice := h.mobileDraft(w, r)
	var err error
	draft, notice, err = h.pruneMobileDraft(r.Context(), id, draft, notice)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "Plan not found")
		return
	}
	if notice != "" {
		h.mobileRedirectError(w, r, "/m", notice)
		return
	}
	if draft.LocationID == 0 || len(draft.ParticipantIDs) == 0 || len(draft.DriverIDs) == 0 {
		h.mobileRedirectError(w, r, "/m", "Choose a location, riders, and drivers first.")
		return
	}
	mode, err := models.ParseRouteMode(draft.Mode)
	if err != nil {
		h.mobileRedirectError(w, r, "/m", messageInvalidRouteMode)
		return
	}
	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(r.Context(), routeCalculationInput{
		ParticipantIDs: draft.ParticipantIDs, DriverIDs: draft.DriverIDs, ActivityLocationID: draft.LocationID,
		RouteTime: draft.RouteTime, Mode: mode, OrgVehicleAssignments: draft.DriverVehicleIDs,
	})
	if outcome.Kind != routeCalculationSuccess {
		message := "Could not calculate routes."
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

func (h *Handler) pruneMobileDraft(ctx context.Context, id string, draft plandraft.Draft, notice string) (plandraft.Draft, string, error) {
	participants, err := h.DB.Participants().GetByIDs(ctx, draft.ParticipantIDs)
	if err != nil {
		return draft, notice, err
	}
	drivers, err := h.DB.Drivers().GetByIDs(ctx, draft.DriverIDs)
	if err != nil {
		return draft, notice, err
	}
	participantSet := make(map[int64]bool, len(participants))
	for _, participant := range participants {
		participantSet[participant.ID] = true
	}
	driverSet := make(map[int64]bool, len(drivers))
	for _, driver := range drivers {
		driverSet[driver.ID] = true
	}
	participantIDs := keepMobileIDs(draft.ParticipantIDs, participantSet)
	driverIDs := keepMobileIDs(draft.DriverIDs, driverSet)
	changed := len(participantIDs) != len(draft.ParticipantIDs) || len(driverIDs) != len(draft.DriverIDs)
	if !changed {
		return draft, notice, nil
	}
	draft = h.PlanDraft.Update(id, func(d *plandraft.Draft) {
		d.ParticipantIDs = participantIDs
		d.DriverIDs = driverIDs
		for driverID := range d.DriverVehicleIDs {
			if !driverSet[driverID] {
				delete(d.DriverVehicleIDs, driverID)
			}
		}
		d.RouteSessionID = ""
	})
	return draft, mergeMobileNotice(notice, "Some unavailable people were removed from this plan."), nil
}

func hiddenMobileIDs(ids []int64, displayed map[int64]bool) []int64 {
	hidden := make([]int64, 0)
	for _, id := range ids {
		if !displayed[id] {
			hidden = append(hidden, id)
		}
	}
	return hidden
}

func keepMobileIDs(ids []int64, available map[int64]bool) []int64 {
	kept := make([]int64, 0, len(ids))
	for _, id := range ids {
		if available[id] {
			kept = append(kept, id)
		}
	}
	return kept
}

func mergeMobileNotice(current, next string) string {
	if current == "" {
		return next
	}
	return current + " " + next
}

func mobileVanAssignmentMessage(err error) string {
	switch err.Error() {
	case invalidVanAssignmentMessage, unselectedDriverVanAssignmentMessage, duplicateVanAssignmentMessage:
		return err.Error()
	default:
		return messageMobileInvalidForm
	}
}
