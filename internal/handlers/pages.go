package handlers

import (
	"net/http"

	"ride-home-router/internal/models"
)

// PageData contains common data for all pages
type PageData struct {
	Title      string
	ActivePage string
}

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

	hasInstituteVehicle := false
	for _, d := range drivers {
		if d.IsInstituteVehicle {
			hasInstituteVehicle = true
			break
		}
	}

	data := map[string]interface{}{
		"Title":               "Event Planning",
		"ActivePage":          "home",
		"Participants":        participants,
		"Drivers":             drivers,
		"HasInstituteVehicle": hasInstituteVehicle,
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

	data := map[string]interface{}{
		"Title":        "Participants",
		"ActivePage":   "participants",
		"Participants": participants,
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

	data := map[string]interface{}{
		"Title":      "Drivers",
		"ActivePage": "drivers",
		"Drivers":    drivers,
	}

	h.renderTemplate(w, "drivers.html", data)
}

// HandleSettingsPage handles GET /settings
func (h *Handler) HandleSettingsPage(w http.ResponseWriter, r *http.Request) {
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	activityLocations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	var selectedLocation *models.ActivityLocation
	if settings.SelectedActivityLocationID > 0 {
		selectedLocation, err = h.DB.ActivityLocations().GetByID(r.Context(), settings.SelectedActivityLocationID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
	}

	data := map[string]interface{}{
		"Title":             "Settings",
		"ActivePage":        "settings",
		"Settings":          settings,
		"ActivityLocations": activityLocations,
		"SelectedLocation":  selectedLocation,
	}

	h.renderTemplate(w, "settings.html", data)
}

// HandleHistoryPage handles GET /history
func (h *Handler) HandleHistoryPage(w http.ResponseWriter, r *http.Request) {
	events, total, err := h.DB.Events().List(r.Context(), 20, 0)
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	// Build events with summaries for the template
	eventsWithSummary := make([]EventWithSummary, len(events))
	for i, event := range events {
		_, _, summary, err := h.DB.Events().GetByID(r.Context(), event.ID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}

		eventsWithSummary[i] = EventWithSummary{
			ID:        event.ID,
			EventDate: event.EventDate,
			Notes:     event.Notes,
			CreatedAt: event.CreatedAt,
			Summary:   summary,
		}
	}

	// Get settings for UseMiles preference
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	data := map[string]interface{}{
		"Title":      "Event History",
		"ActivePage": "history",
		"Events":     eventsWithSummary,
		"Total":      total,
		"UseMiles":   settings.UseMiles,
	}

	h.renderTemplate(w, "history.html", data)
}
