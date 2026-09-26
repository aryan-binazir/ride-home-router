package handlers

import (
	"log"
	"net/http"
	"ride-home-router/internal/logutil"
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
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to confirm participant address: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}
	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Confirmed participant address: id=%d", id)
	if !h.isHTMX(r) {
		response, err := h.participantResponse(r.Context(), participant)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}
	participants, err := h.DB.Participants().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	view, err := h.participantListView(r, participants)
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
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to confirm driver address: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}
	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Confirmed driver address: id=%d", id)
	if !h.isHTMX(r) {
		response, err := h.driverResponse(r.Context(), driver)
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		h.writeJSON(w, http.StatusOK, response)
		return
	}
	drivers, err := h.DB.Drivers().List(r.Context(), "")
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	view, err := h.driverListView(r, drivers)
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	h.setHTMXToastWithEvent(w, "driverUpdated", messageAddressConfirmed, toastTypeSuccess)
	h.renderTemplate(w, "driver_list", view)
}

func confirmedAddress(typed, geocoderLabel string) (string, string) {
	if strings.TrimSpace(geocoderLabel) != "" {
		typed = geocoderLabel
	}
	return typed, models.AddressMatchConfirmed
}
