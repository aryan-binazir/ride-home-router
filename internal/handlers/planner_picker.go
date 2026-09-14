package handlers

import (
	"net/http"
	"ride-home-router/internal/models"
	"slices"
	"strconv"
	"strings"
)

const rosterPageSize = 50

func pickerWindow(total, requested int) (start, end, next, previous int) {
	start = max(0, requested)
	if total == 0 {
		start = 0
	} else if start >= total {
		start = (total - 1) / rosterPageSize * rosterPageSize
	}
	end = min(total, start+rosterPageSize)
	if end < total {
		next = end
	}
	previous = max(0, start-rosterPageSize)
	return
}

// HandlePlannerPicker renders one bounded roster page and compact selected
// values outside it. It never changes a stored plan or computes routes.
func (h *Handler) HandlePlannerPicker(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/api/v1/planner/")
	if kind != "drivers" && kind != "participants" {
		h.handleNotFoundHTMX(w, r, "Picker not found")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	name := "participant_ids"
	if kind == "drivers" {
		name = "driver_ids"
	}
	ids, ok := parseMobileSelection(r.Form[name])
	if !ok {
		h.handleValidationErrorHTMX(w, r, mobileSelectionLimitMessage())
		return
	}
	selected := mobileSelected(ids)
	labels := parseMobileIDs(r.Form["label_ids"])
	search := strings.TrimSpace(r.Form.Get("search"))
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	view := IndexPageView{PagedPickers: true, AssignedVehicles: map[int64]*models.OrganizationVehicle{}}
	matchesLabel := func(id int64, memberships map[int64][]int64) bool {
		if len(labels) == 0 {
			return true
		}
		for _, label := range memberships[id] {
			if slices.Contains(labels, label) {
				return true
			}
		}
		return false
	}
	if kind == "participants" {
		people, err := h.DB.Participants().List(r.Context(), search)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		memberships, err := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		filtered := make([]models.Participant, 0, len(people))
		for _, person := range people {
			if matchesLabel(person.ID, memberships) {
				filtered = append(filtered, person)
				if r.Form.Get("select") == "all" {
					selected[person.ID] = true
				}
			}
		}
		ids = selectedPickerIDs(selected)
		if _, ok := parseMobileSelection(int64Strings(ids)); !ok {
			h.handleValidationErrorHTMX(w, r, mobileSelectionLimitMessage())
			return
		}
		chosen, err := h.DB.Participants().GetByIDs(r.Context(), ids)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		start, end, next, previous := pickerWindow(len(filtered), offset)
		view.Participants = filtered[start:end]
		view.ParticipantNext = next
		view.PickerOffset = start
		view.PickerPrevious = previous
		view.ParticipantLabels = memberships
		view.SelectedParticipants = map[int64]bool{}
		visible := map[int64]bool{}
		for _, person := range view.Participants {
			visible[person.ID] = true
		}
		for _, person := range chosen {
			view.SelectedParticipants[person.ID] = true
			if !visible[person.ID] {
				view.HiddenParticipants = append(view.HiddenParticipants, person.ID)
			}
		}
	} else {
		drivers, err := h.DB.Drivers().List(r.Context(), search)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		memberships, err := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		filtered := make([]models.Driver, 0, len(drivers))
		for _, driver := range drivers {
			if matchesLabel(driver.ID, memberships) {
				filtered = append(filtered, driver)
				if r.Form.Get("select") == "all" {
					selected[driver.ID] = true
				}
			}
		}
		ids = selectedPickerIDs(selected)
		if _, ok := parseMobileSelection(int64Strings(ids)); !ok {
			h.handleValidationErrorHTMX(w, r, mobileSelectionLimitMessage())
			return
		}
		chosen, err := h.DB.Drivers().GetByIDs(r.Context(), ids)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		assignments, err := parseOrgVehicleAssignments(r.Form, ids)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, mobileVanAssignmentMessage(err))
			return
		}
		vehicleIDs := make([]int64, 0, len(assignments))
		for _, id := range assignments {
			vehicleIDs = append(vehicleIDs, id)
		}
		vehicles, err := h.DB.OrganizationVehicles().GetByIDs(r.Context(), vehicleIDs)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		byID := map[int64]models.OrganizationVehicle{}
		for _, vehicle := range vehicles {
			byID[vehicle.ID] = vehicle
		}
		for driverID, vehicleID := range assignments {
			if vehicle, ok := byID[vehicleID]; ok {
				view.AssignedVehicles[driverID] = &vehicle
			}
		}
		start, end, next, previous := pickerWindow(len(filtered), offset)
		view.Drivers = filtered[start:end]
		view.DriverNext = next
		view.PickerOffset = start
		view.PickerPrevious = previous
		view.DriverLabels = memberships
		view.SelectedDrivers = map[int64]bool{}
		visible := map[int64]bool{}
		for _, driver := range view.Drivers {
			visible[driver.ID] = true
		}
		for _, driver := range chosen {
			view.SelectedDrivers[driver.ID] = true
			if !visible[driver.ID] {
				view.HiddenDrivers = append(view.HiddenDrivers, driver)
			}
		}
	}
	h.renderTemplate(w, "index.html#planner_"+kind, view)
}

func selectedPickerIDs(selected map[int64]bool) []int64 {
	ids := make([]int64, 0, len(selected))
	for id, on := range selected {
		if on {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func int64Strings(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}
