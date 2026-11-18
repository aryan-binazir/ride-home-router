package handlers

import (
	"log/slog"
	"net/http"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

const AddressConfirmSuffix = "/address/confirm"

func addressConfirmID(path, prefix string) (int64, bool) {
	idStr := strings.TrimSuffix(strings.TrimPrefix(path, prefix), AddressConfirmSuffix)
	id, err := strconv.ParseInt(idStr, 10, 64)
	return id, err == nil && id > 0
}

func (h *Handler) HandleConfirmParticipantAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := addressConfirmID(r.URL.Path, "/api/v1/participants/")
	if !ok {
		h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
		return
	}
	participant, err := h.DB.Participants().GetByID(r.Context(), id)
	if err == nil {
		participant.Address, participant.AddressMatch = confirmedAddress(participant.Address, participant.MatchedAddress)
		participant, err = h.DB.Participants().Update(r.Context(), participant)
	}
	if h.checkNotFound(err) {
		if h.isHTMX(r) {
			h.handleNotFoundHTMX(w, r, messageParticipantNotFound)
			return
		}
		h.handleNotFound(w, r, messageParticipantNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to confirm participant address", "id", id, "error", err.Error())
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}
	slog.Info("Confirmed participant address", "component", "http", "id", id)
	if !h.isHTMX(r) {
		response, err := participantRoster.responseWithLabels(h.DB, r.Context(), participant)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}
	view, err := (rosterReader{db: h.DB}).participants(r)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.setHTMXToastWithEvent(w, "participantUpdated", messageAddressConfirmed, toastTypeSuccess)
	h.renderTemplate(w, "participant_list", view)
}

func (h *Handler) HandleConfirmDriverAddress(w http.ResponseWriter, r *http.Request) {
	id, ok := addressConfirmID(r.URL.Path, "/api/v1/drivers/")
	if !ok {
		h.handleValidationErrorHTMX(w, r, messageInvalidDriverID)
		return
	}
	driver, err := h.DB.Drivers().GetByID(r.Context(), id)
	if err == nil {
		driver.Address, driver.AddressMatch = confirmedAddress(driver.Address, driver.MatchedAddress)
		driver, err = h.DB.Drivers().Update(r.Context(), driver)
	}
	if h.checkNotFound(err) {
		if h.isHTMX(r) {
			h.handleNotFoundHTMX(w, r, messageDriverNotFound)
			return
		}
		h.handleNotFound(w, r, messageDriverNotFound)
		return
	}
	if err != nil {
		slog.Error("Failed to confirm driver address", "id", id, "error", err.Error())
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}
	slog.Info("Confirmed driver address", "component", "http", "id", id)
	if !h.isHTMX(r) {
		response, err := driverRoster.responseWithLabels(h.DB, r.Context(), driver)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}
	view, err := (rosterReader{db: h.DB}).drivers(r)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.setHTMXToastWithEvent(w, "driverUpdated", messageAddressConfirmed, toastTypeSuccess)
	h.renderTemplate(w, "driver_list", view)
}

func confirmedAddress(typed, geocoderLabel string) (string, models.AddressMatch) {
	if strings.TrimSpace(geocoderLabel) != "" {
		typed = geocoderLabel
	}
	return typed, models.AddressMatchConfirmed
}
