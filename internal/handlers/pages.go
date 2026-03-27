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

	h.renderTemplate(w, "index.html", IndexPageView{
		BasePageView: BasePageView{
			Title:      "Event Planning",
			ActivePage: ActivePageHome,
		},
		Participants:      participants,
		Drivers:           drivers,
		ActivityLocations: activityLocations,
		OrgVehicles:       orgVehicles,
	})
}

// HandleParticipantsPage handles GET /participants
func (h *Handler) HandleParticipantsPage(w http.ResponseWriter, r *http.Request) {
	participants, err := h.DB.Participants().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "participants.html", ParticipantsPageView{
		BasePageView: BasePageView{
			Title:      "Participants",
			ActivePage: ActivePageParticipants,
		},
		Participants: participants,
	})
}

// HandleDriversPage handles GET /drivers
func (h *Handler) HandleDriversPage(w http.ResponseWriter, r *http.Request) {
	drivers, err := h.DB.Drivers().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "drivers.html", DriversPageView{
		BasePageView: BasePageView{
			Title:      "Drivers",
			ActivePage: ActivePageDrivers,
		},
		Drivers: drivers,
	})
}

// HandleActivityLocationsPage handles GET /activity-locations
func (h *Handler) HandleActivityLocationsPage(w http.ResponseWriter, r *http.Request) {
	activityLocations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "activity_locations.html", ActivityLocationsPageView{
		BasePageView: BasePageView{
			Title:      "Activity Locations",
			ActivePage: ActivePageActivityLocations,
		},
		ActivityLocations: activityLocations,
	})
}

// HandleVansPage handles GET /vans
func (h *Handler) HandleVansPage(w http.ResponseWriter, r *http.Request) {
	orgVehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "vans.html", VansPageView{
		BasePageView: BasePageView{
			Title:      "Vans",
			ActivePage: ActivePageVans,
		},
		OrgVehicles: orgVehicles,
	})
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

	h.renderTemplate(w, "settings.html", SettingsPageView{
		BasePageView: BasePageView{
			Title:      "Settings",
			ActivePage: ActivePageSettings,
		},
		Settings: settings,
		DatabaseConfig: DatabaseConfigView{
			DatabasePath: dbConfig.DatabasePath,
			DefaultPath:  defaultDBPath,
			IsDefault:    dbConfig.DatabasePath == defaultDBPath,
		},
	})
}

// HandleHistoryPage handles GET /history
func (h *Handler) HandleHistoryPage(w http.ResponseWriter, r *http.Request) {
	view, err := h.buildEventListView(r.Context(), 20, 0)
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "history.html", HistoryPageView{
		BasePageView: BasePageView{
			Title:      "Event History",
			ActivePage: ActivePageHistory,
		},
		Events:         view.Events,
		Total:          view.Total,
		UseMiles:       view.UseMiles,
		Limit:          view.Limit,
		Offset:         view.Offset,
		DisplayedCount: view.DisplayedCount,
		NextOffset:     view.NextOffset,
		PageSize:       view.PageSize,
	})
}
