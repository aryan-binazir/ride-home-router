package handlers

import (
	"net/http"

	"ride-home-router/internal/database"
)

// HandleIndexPage handles GET /
func (h *Handler) HandleIndexPage(w http.ResponseWriter, r *http.Request) {
	participants, err := h.DB.Participants().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	drivers, err := h.DB.Drivers().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	activityLocations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	orgVehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	participantGroupIDs, err := h.DB.Groups().ListGroupIDsForParticipants(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	driverGroupIDs, err := h.DB.Groups().ListGroupIDsForDrivers(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":               "Event Planning",
		"ActivePage":          "home",
		"Participants":        participants,
		"Drivers":             drivers,
		"ActivityLocations":   activityLocations,
		"OrgVehicles":         orgVehicles,
		"Groups":              groups,
		"ParticipantGroupIDs": participantGroupIDs,
		"DriverGroupIDs":      driverGroupIDs,
	}

	h.renderTemplate(w, "index.html", data)
}

// HandleParticipantsPage handles GET /participants
func (h *Handler) HandleParticipantsPage(w http.ResponseWriter, r *http.Request) {
	participants, err := h.DB.Participants().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	participantGroupIDs, err := h.DB.Groups().ListGroupIDsForParticipants(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":               "Participants",
		"ActivePage":          "participants",
		"Participants":        participants,
		"Groups":              groups,
		"ParticipantGroupIDs": participantGroupIDs,
	}

	h.renderTemplate(w, "participants.html", data)
}

// HandleDriversPage handles GET /drivers
func (h *Handler) HandleDriversPage(w http.ResponseWriter, r *http.Request) {
	drivers, err := h.DB.Drivers().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	driverGroupIDs, err := h.DB.Groups().ListGroupIDsForDrivers(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":          "Drivers",
		"ActivePage":     "drivers",
		"Drivers":        drivers,
		"Groups":         groups,
		"DriverGroupIDs": driverGroupIDs,
	}

	h.renderTemplate(w, "drivers.html", data)
}

// HandleActivityLocationsPage handles GET /activity-locations
func (h *Handler) HandleActivityLocationsPage(w http.ResponseWriter, r *http.Request) {
	activityLocations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":             "Activity Locations",
		"ActivePage":        "activity_locations",
		"ActivityLocations": activityLocations,
	}

	h.renderTemplate(w, "activity_locations.html", data)
}

// HandleVansPage handles GET /vans
func (h *Handler) HandleVansPage(w http.ResponseWriter, r *http.Request) {
	orgVehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":       "Vans",
		"ActivePage":  "vans",
		"OrgVehicles": orgVehicles,
	}

	h.renderTemplate(w, "vans.html", data)
}

// HandleSettingsPage handles GET /settings
func (h *Handler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	// Load database config
	dbConfig, err := database.LoadConfig()
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	defaultDBPath, _ := database.GetDefaultDBPath()

	data := map[string]any{
		"Title":      "Settings",
		"ActivePage": "settings",
		"Settings":   settings,
		"DatabaseConfig": map[string]any{
			"DatabasePath": dbConfig.DatabasePath,
			"DefaultPath":  defaultDBPath,
			"IsDefault":    dbConfig.DatabasePath == defaultDBPath,
		},
	}

	h.renderTemplate(w, "settings.html", data)
}

// HandleHistoryPage handles GET /history
func (h *Handler) HandleHistoryPage(w http.ResponseWriter, r *http.Request) {
	view, err := h.buildEventListView(r.Context(), 20, 0)
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]any{
		"Title":          "Event History",
		"ActivePage":     "history",
		"Events":         view.Events,
		"Total":          view.Total,
		"UseMiles":       view.UseMiles,
		"Limit":          view.Limit,
		"Offset":         view.Offset,
		"DisplayedCount": view.DisplayedCount,
		"NextOffset":     view.NextOffset,
		"PageSize":       view.PageSize,
	}

	h.renderTemplate(w, "history.html", data)
}
