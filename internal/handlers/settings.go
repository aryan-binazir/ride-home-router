package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/access"
	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	slog.Info("HTTP request", "component", "http", "method", "GET", "path", "/api/v1/settings")
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		slog.Error("Failed to get settings", "error", fmt.Sprint(err))
		h.handleInternalError(w, r, err)
		return
	}

	if !access.IsAdmin(r.Context()) {
		settings.SMEEmail = ""
	}
	h.writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SelectedActivityLocationID *int64  `json:"selected_activity_location_id"`
		UseMiles                   bool    `json:"use_miles"`
		SMEEmail                   *string `json:"sme_email"`
		CollectReviewerNotes       *bool   `json:"collect_reviewer_notes"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			slog.Error("Failed to parse form", "error", fmt.Sprint(err))
			h.setHTMXToast(w, messageInvalidFormData, toastTypeError)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if idStr := r.FormValue("selected_activity_location_id"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				req.SelectedActivityLocationID = &id
			}
		}
		req.UseMiles = r.FormValue("use_miles") == "on" || r.FormValue("use_miles") == "true"
		if email, collectNotes, submitted := reviewerFieldsFromForm(r.Form); submitted {
			req.SMEEmail = &email
			req.CollectReviewerNotes = &collectNotes
		}
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			slog.Info("Invalid request body", "component", "http", "method", "PUT", "path", "/api/v1/settings", "error", fmt.Sprint(err))
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
	}

	if (req.SMEEmail != nil || req.CollectReviewerNotes != nil) && !access.IsAdmin(r.Context()) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusForbidden, "FORBIDDEN", "Only administrators can change reviewer settings.")
		return
	}

	var err error
	var selectedActivityLocationID int64
	var location *models.ActivityLocation

	if req.SelectedActivityLocationID != nil {
		selectedActivityLocationID = *req.SelectedActivityLocationID

		if selectedActivityLocationID > 0 {
			location, err = h.DB.ActivityLocations().GetByID(r.Context(), selectedActivityLocationID)
			if err != nil {
				if h.checkNotFound(err) {
					h.handleSelectedActivityLocationNotFound(w, r, selectedActivityLocationID)
					return
				}
				slog.Error("Failed to get activity location", "error", fmt.Sprint(err))
				if h.isHTMX(r) {
					h.setHTMXToast(w, messageGenericInternalError, toastTypeError)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				h.handleInternalError(w, r, err)
				return
			}
		}
	}

	if req.SMEEmail != nil {
		email := strings.TrimSpace(*req.SMEEmail)
		req.SMEEmail = &email
		if email != "" {
			address, parseErr := mail.ParseAddress(email)
			if parseErr != nil || address.Address != email {
				h.handleValidationErrorHTMX(w, r, messageInvalidSMEEmail)
				return
			}
		}
	}

	settings, err := h.DB.Settings().Patch(r.Context(), database.SettingsPatch{SelectedActivityLocationID: req.SelectedActivityLocationID, UseMiles: req.UseMiles, SMEEmail: req.SMEEmail, CollectReviewerNotes: req.CollectReviewerNotes})
	if err != nil {
		if h.checkNotFound(err) {
			h.handleSelectedActivityLocationNotFound(w, r, selectedActivityLocationID)
			return
		}
		slog.Error("Failed to update settings", "error", fmt.Sprint(err))
		if h.isHTMX(r) {
			h.setHTMXToast(w, messageGenericInternalError, toastTypeError)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	slog.Info("Updated settings", "component", "http", "selected_location_id", settings.SelectedActivityLocationID)
	if h.isHTMX(r) {
		message := messagePreferencesSaved
		if location != nil {
			message = messageSettingsSavedUsing(location.Name)
		}
		h.setHTMXToast(w, message, toastTypeSuccess)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !access.IsAdmin(r.Context()) {
		settings.SMEEmail = ""
	}
	h.writeJSON(w, http.StatusOK, settings)
}

func reviewerFieldsFromForm(form url.Values) (email string, collectNotes bool, submitted bool) {
	if !form.Has("sme_email") {
		return "", false, false
	}
	checked := form.Get("collect_reviewer_notes")
	return form.Get("sme_email"), checked == "on" || checked == "true", true
}

func (h *Handler) handleSelectedActivityLocationNotFound(w http.ResponseWriter, r *http.Request, id int64) {
	slog.Info("Activity location not found", "component", "http", "method", "PUT", "path", "/api/v1/settings", "id", id)
	h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageSelectedActivityLocationNotFound)
}
