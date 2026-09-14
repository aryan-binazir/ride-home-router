package handlers

import (
	"maps"
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

type vehicleEditorView struct {
	Driver      models.Driver
	Vehicle     *models.OrganizationVehicle
	Selected    bool
	Mobile      bool
	Shortage    bool
	Choices     []editorChoice
	Search      string
	URL         string
	Include     string
	PreviousURL string
	NextURL     string
}

// HandleVehicleAssignments resolves only saved selections, not the vehicle catalog.
func (h *Handler) HandleVehicleAssignments(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	ids, ok := parseMobileSelection(r.Form["driver_ids"])
	if !ok {
		h.handleValidationErrorHTMX(w, r, mobileSelectionLimitMessage())
		return
	}
	assignments, err := parseOrgVehicleAssignments(r.Form, ids)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, mobileVanAssignmentMessage(err))
		return
	}
	var driverIDs, vehicleIDs []int64
	for _, id := range ids {
		if vehicleID := assignments[id]; vehicleID > 0 {
			driverIDs = append(driverIDs, id)
			vehicleIDs = append(vehicleIDs, vehicleID)
		}
	}
	drivers, err := h.DB.Drivers().GetByIDs(r.Context(), driverIDs)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	vehicles, err := h.DB.OrganizationVehicles().GetByIDs(r.Context(), vehicleIDs)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	byID := make(map[int64]models.OrganizationVehicle, len(vehicles))
	for _, vehicle := range vehicles {
		byID[vehicle.ID] = vehicle
	}
	views := make([]vehicleEditorView, 0, len(drivers))
	for _, driver := range drivers {
		view := vehicleEditorView{Driver: driver, Selected: true}
		if vehicle, exists := byID[assignments[driver.ID]]; exists {
			view.Vehicle = &vehicle
		}
		views = append(views, view)
	}
	h.renderTemplate(w, "vehicle_assignments", views)
}

// HandleVehicleEditor renders a bounded catalog or one chosen vehicle control.
// This edits the submitted plan form, never the roster or a stored route session.
func (h *Handler) HandleVehicleEditor(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("driver_id"), 10, 64)
	if err != nil || id <= 0 {
		h.handleValidationErrorHTMX(w, r, messageInvalidDriverID)
		return
	}
	driver, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	if driver == nil {
		h.handleNotFoundHTMX(w, r, messageInvalidDriverID)
		return
	}
	view := vehicleEditorView{Driver: *driver, Selected: true, Mobile: r.URL.Query().Get("mobile") == "1", Shortage: r.URL.Query().Get("shortage") == "1", Search: strings.TrimSpace(r.Form.Get("search"))}
	view.Include = "#event-form .van-assignment-select"
	prefix := ""
	if view.Mobile {
		view.Include = "#mobile-driver-picker [name^='org_vehicle_']"
		prefix = "mobile-"
	} else if view.Shortage {
		view.Include = "#recalc-form .org-vehicle-select"
		prefix = "shortage-"
	}
	q := url.Values{"driver_id": {strconv.FormatInt(id, 10)}}
	if view.Mobile {
		q.Set("mobile", "1")
	} else if view.Shortage {
		q.Set("shortage", "1")
	}
	view.URL = "/api/v1/planner/vehicle-editor?" + q.Encode()
	taken := map[int64]bool{}
	for key, values := range r.PostForm {
		if !strings.HasPrefix(key, "org_vehicle_") || key == "org_vehicle_"+strconv.FormatInt(id, 10) {
			continue
		}
		for _, value := range values {
			vehicleID, _ := strconv.ParseInt(value, 10, 64)
			if vehicleID > 0 {
				taken[vehicleID] = true
			}
		}
	}
	if r.PostForm.Get("choose") == "1" {
		vehicleID, parseErr := strconv.ParseInt(r.PostForm.Get("vehicle_id"), 10, 64)
		if parseErr != nil || vehicleID < 0 || taken[vehicleID] {
			h.handleValidationErrorHTMX(w, r, "Choose an available vehicle.")
			return
		}
		if vehicleID > 0 {
			vehicle, getErr := h.DB.OrganizationVehicles().GetByID(r.Context(), vehicleID)
			if getErr != nil {
				h.handleInternalError(w, r, getErr)
				return
			}
			if vehicle == nil {
				h.handleValidationErrorHTMX(w, r, "That vehicle is no longer available.")
				return
			}
			view.Vehicle = vehicle
		}
		w.Header().Set("HX-Retarget", "#"+prefix+"van-control-"+strconv.FormatInt(id, 10))
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderTemplate(w, "vehicle_assignment", view)
		return
	}
	vehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	offset, _ := strconv.Atoi(r.Form.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	matches := 0
	for _, vehicle := range vehicles {
		if taken[vehicle.ID] || !strings.Contains(strings.ToLower(vehicle.Name), strings.ToLower(view.Search)) {
			continue
		}
		if matches >= offset && len(view.Choices) < editorPageSize {
			view.Choices = append(view.Choices, editorChoice{vehicle.ID, vehicle.Name, strconv.Itoa(vehicle.Capacity) + " seats"})
		}
		matches++
	}
	pageURL := func(next int) string {
		page := maps.Clone(q)
		page.Set("offset", strconv.Itoa(next))
		page.Set("search", view.Search)
		return "/api/v1/planner/vehicle-editor?" + page.Encode()
	}
	if offset > 0 {
		view.PreviousURL = pageURL(max(0, offset-editorPageSize))
	}
	if offset < matches-editorPageSize {
		view.NextURL = pageURL(offset + editorPageSize)
	}
	h.renderTemplate(w, "vehicle_editor", view)
}
