package handlers

import (
	"log"
	"net/http"
	"net/mail"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

// HandleGetSettings handles GET /api/v1/settings
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/settings")
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to get settings: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

// HandleUpdateSettings handles PUT /api/v1/settings
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SelectedActivityLocationID *int64  `json:"selected_activity_location_id"`
		UseMiles                   bool    `json:"use_miles"`
		SMEEmail                   *string `json:"sme_email"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			h.setHTMXToast(w, err.Error(), toastTypeError)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if idStr := r.FormValue("selected_activity_location_id"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				req.SelectedActivityLocationID = &id
			}
		}
		req.UseMiles = r.FormValue("use_miles") == "on" || r.FormValue("use_miles") == "true"
		if r.Form.Has("sme_email") {
			value := r.FormValue("sme_email")
			req.SMEEmail = &value
		}
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/settings: invalid_body err=%v", err)
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
	}

	currentSettings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to get existing settings: err=%v", err)
		if h.isHTMX(r) {
			h.setHTMXToast(w, err.Error(), toastTypeError)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	selectedActivityLocationID := currentSettings.SelectedActivityLocationID
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
				log.Printf("[ERROR] Failed to get activity location: err=%v", err)
				if h.isHTMX(r) {
					h.setHTMXToast(w, err.Error(), toastTypeError)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				h.handleInternalError(w, err)
				return
			}
		}
	}

	settings := &models.Settings{
		SelectedActivityLocationID: selectedActivityLocationID,
		UseMiles:                   req.UseMiles,
		SMEEmail:                   currentSettings.SMEEmail,
	}
	if req.SMEEmail != nil {
		settings.SMEEmail = strings.TrimSpace(*req.SMEEmail)
		if settings.SMEEmail != "" {
			address, parseErr := mail.ParseAddress(settings.SMEEmail)
			if parseErr != nil || address.Address != settings.SMEEmail {
				h.handleValidationErrorHTMX(w, r, messageInvalidSMEEmail)
				return
			}
		}
	}

	if err := h.DB.Settings().Update(r.Context(), settings); err != nil {
		if h.checkNotFound(err) {
			h.handleSelectedActivityLocationNotFound(w, r, selectedActivityLocationID)
			return
		}
		log.Printf("[ERROR] Failed to update settings: err=%v", err)
		if h.isHTMX(r) {
			h.setHTMXToast(w, err.Error(), toastTypeError)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated settings: selected_location_id=%d", settings.SelectedActivityLocationID)
	if h.isHTMX(r) {
		message := messagePreferencesSaved
		if location != nil {
			message = messageSettingsSavedUsing(location.Name)
		}
		h.setHTMXToast(w, message, toastTypeSuccess)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

func (h *Handler) handleSelectedActivityLocationNotFound(w http.ResponseWriter, r *http.Request, id int64) {
	log.Printf("[HTTP] PUT /api/v1/settings: activity location not found: id=%d", id)
	h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageSelectedActivityLocationNotFound)
}
