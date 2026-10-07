package handlers

import (
	"net/http"

	"ride-home-router/internal/access"
)

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

	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	participantLabels, err := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	driverLabels, err := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	_, participantEnd, participantNext, _ := pickerWindow(len(participants), 0)
	_, driverEnd, driverNext, _ := pickerWindow(len(drivers), 0)
	participants = participants[:participantEnd]
	drivers = drivers[:driverEnd]
	h.renderTemplate(w, "index.html", IndexPageView{
		PagedPickers: true, ParticipantNext: participantNext, DriverNext: driverNext,
		Title:             "Event Planning",
		ActivePage:        ActivePageHome,
		Participants:      participants,
		Drivers:           drivers,
		Labels:            labels,
		ParticipantLabels: participantLabels,
		DriverLabels:      driverLabels,
		ActivityLocations: activityLocations,
	})
}

func (h *Handler) HandleParticipantsPage(w http.ResponseWriter, r *http.Request) {
	view, err := (rosterReader{db: h.DB}).participants(r)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderTemplate(w, "participants.html", ParticipantsPageView{
		Pagination:   view.Pagination,
		Title:        "Participants",
		ActivePage:   ActivePageParticipants,
		Participants: view.Participants,
		Labels:       view.Labels,
		LabelIDs:     view.LabelIDs,
	})
}

func (h *Handler) HandleDriversPage(w http.ResponseWriter, r *http.Request) {
	view, err := (rosterReader{db: h.DB}).drivers(r)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.renderTemplate(w, "drivers.html", DriversPageView{
		Pagination: view.Pagination,
		Title:      "Drivers",
		ActivePage: ActivePageDrivers,
		Drivers:    view.Drivers,
		Labels:     view.Labels,
		LabelIDs:   view.LabelIDs,
	})
}

func (h *Handler) HandleLabelsPage(w http.ResponseWriter, r *http.Request) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "labels.html", LabelsPageView{
		Title:      "Labels",
		ActivePage: ActivePageLabels,
		Labels:     labels,
	})
}

func (h *Handler) HandleActivityLocationsPage(w http.ResponseWriter, r *http.Request) {
	activityLocations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "activity_locations.html", ActivityLocationsPageView{
		Title:             "Activity Locations",
		ActivePage:        ActivePageActivityLocations,
		ActivityLocations: activityLocations,
	})
}

func (h *Handler) HandleVansPage(w http.ResponseWriter, r *http.Request) {
	orgVehicles, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "vans.html", VansPageView{
		Title:       "Vans",
		ActivePage:  ActivePageVans,
		OrgVehicles: orgVehicles,
	})
}

func (h *Handler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "settings.html", SettingsPageView{
		IsAdmin:    access.IsAdmin(r.Context()),
		Title:      "Settings",
		ActivePage: ActivePageSettings,
		Settings:   settings,
	})
}

func (h *Handler) HandleHistoryPage(w http.ResponseWriter, r *http.Request) {
	view, err := h.buildEventListView(r.Context(), 20, 0)
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "history.html", HistoryPageView{
		Title:          "Event History",
		ActivePage:     ActivePageHistory,
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
