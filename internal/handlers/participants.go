package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

// ParticipantListResponse represents the list response
type ParticipantListResponse struct {
	Participants []ParticipantResponse `json:"participants"`
	Total        int                   `json:"total"`
}

// ParticipantResponse represents a participant API response.
type ParticipantResponse struct {
	models.Participant
	LabelIDs []int64 `json:"label_ids"`
}

// HandleListParticipants handles GET /api/v1/participants
func (h *Handler) HandleListParticipants(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")
	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] GET /api/v1/participants:")

	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to list participants: err=%s", logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Listed participants: count=%d", len(participants))
	if h.isHTMX(r) {
		view, err := h.participantListView(r, participants)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "participant_list", view)
		return
	}

	responseParticipants, err := h.participantResponses(r.Context(), participants)
	if err != nil {
		log.Printf("[ERROR] Failed to load participant labels for list: err=%v", err)
		h.handleInternalError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, ParticipantListResponse{
		Participants: responseParticipants,
		Total:        len(participants),
	})
}

// HandleListDeletedParticipants handles GET /api/v1/participants/deleted.
func (h *Handler) HandleListDeletedParticipants(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/participants/deleted")

	participants, err := h.DB.Participants().ListDeleted(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to list deleted participants: err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	log.Printf("[HTTP] Listed deleted participants: count=%d", len(participants))
	if h.isHTMX(r) {
		view, err := h.participantListView(r, participants)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "participant_deleted_list", view)
		return
	}

	responseParticipants, err := h.participantResponses(r.Context(), participants)
	if err != nil {
		log.Printf("[ERROR] Failed to load deleted participant labels: err=%v", err)
		h.handleInternalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, ParticipantListResponse{
		Participants: responseParticipants,
		Total:        len(participants),
	})
}

// HandleGetParticipant handles GET /api/v1/participants/{id}
func (h *Handler) HandleGetParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] GET /api/v1/participants/{id}: invalid_id=%s err=%s", logutil.SafeString(idStr), logutil.SafeString(err.Error()))
		h.handleValidationError(w, r, "Choose a valid rider. Refresh the page and try again.")
		return
	}

	log.Printf("[HTTP] GET /api/v1/participants/{id}: id=%d", id)
	participant, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found: id=%d", id)
			h.handleNotFound(w, r, "Rider not found. Refresh the page and try again.")
			return
		}
		log.Printf("[ERROR] Failed to get participant: id=%d err=%v", id, err)
		h.handleInternalError(w, r, err)
		return
	}

	response, err := h.participantResponse(r.Context(), participant)
	if err != nil {
		log.Printf("[ERROR] Failed to load participant labels: id=%d err=%v", participant.ID, err)
		h.handleInternalError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleCreateParticipant handles POST /api/v1/participants
func (h *Handler) HandleCreateParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string  `json:"name"`
		Address     string  `json:"address"`
		AddressName string  `json:"address_name"`
		LabelIDs    []int64 `json:"label_ids"`
	}
	var labelIDs []int64

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
		req.AddressName = r.FormValue("address_name")
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
			return
		}
		labelIDs = parsedLabelIDs
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			log.Printf("[HTTP] POST /api/v1/participants: invalid_body err=%v", err)
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
		labelIDs = req.LabelIDs
	}
	req.AddressName = strings.TrimSpace(req.AddressName)

	if message := personLengthMessage(req.Name, req.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" || req.Address == "" {
		//nolint:gosec // G706: request fields are logged only as presence booleans.
		log.Printf("[HTTP] POST /api/v1/participants: missing_name=%t missing_address=%t", req.Name == "", req.Address == "")
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageNameAndAddressRequired)
			return
		}
		h.handleValidationError(w, r, messageNameAndAddressRequired)
		return
	}
	if len([]rune(req.AddressName)) > models.MaxAddressNameLength {
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageAddressNameTooLong())
			return
		}
		h.handleValidationError(w, r, messageAddressNameTooLong())
		return
	}
	if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
		log.Printf("[HTTP] POST /api/v1/participants: invalid_labels err=%v", err)
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
			return
		}
		h.handleValidationError(w, r, messageInvalidLabelSelection)
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Print("[HTTP] POST /api/v1/participants:")
	participant, err := (rosterEditor{db: h.DB, geocoder: h.Geocoder}).createParticipant(r.Context(), participantEdit{
		Name: req.Name, Address: req.Address, AddressName: req.AddressName, LabelIDs: labelIDs,
	})
	if duplicate, ok := errors.AsType[rosterDuplicateError](err); ok {
		h.handleHTMXErrorNoSwap(w, r, http.StatusConflict, "DUPLICATE_ROSTER_ENTRY", duplicate.Error())
		return
	}
	if geocodeErr, ok := errors.AsType[rosterGeocodeError](err); ok {
		log.Print("[ERROR] Failed to geocode participant address")
		if h.isHTMX(r) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(geocodeErr.err))
			return
		}
		h.handleGeocodingError(w, r, geocodeErr.err)
		return
	}

	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to create participant: err=%s", logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] Created participant: id=%d", participant.ID)
	if h.isHTMX(r) {
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list participants after create: err=%v", err)
			h.setHTMXToastWithEvent(w, "participantCreated", "Participant saved. Refresh the page to see the updated roster.", toastTypeWarning)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.setHTMXToastWithEvent(w, "participantCreated", messageEntityAdded("Participant", participant.Name), toastTypeSuccess)
		view, err := h.participantListView(r, participants)
		if err != nil {
			h.setHTMXToastWithEvent(w, "participantCreated", "Participant saved. Refresh the page to see the updated roster.", toastTypeWarning)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.renderTemplate(w, "participant_list", view)
		return
	}

	// Creation committed the validated label IDs atomically with the person.
	// A response must not depend on another read that can fail after that commit.
	uniqueLabelIDs, _ := uniquePositiveIDs(labelIDs)
	h.writeJSON(w, http.StatusCreated, ParticipantResponse{Participant: *participant, LabelIDs: uniqueLabelIDs})
}

// HandleUpdateParticipant handles PUT /api/v1/participants/{id}
func (h *Handler) HandleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	if trimmedID, ok := strings.CutSuffix(idStr, "/edit"); ok {
		idStr = trimmedID
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] PUT /api/v1/participants/{id}: invalid_id=%s err=%s", logutil.SafeString(idStr), logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
			return
		}
		h.handleValidationError(w, r, messageInvalidParticipantID)
		return
	}

	log.Printf("[HTTP] PUT /api/v1/participants/{id}: id=%d", id)

	existing, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found for update: id=%d", id)
			if h.isHTMX(r) {
				h.handleNotFoundHTMX(w, r, messageParticipantNotFound)
				return
			}
			h.handleNotFound(w, r, messageParticipantNotFound)
			return
		}
		log.Printf("[ERROR] Failed to get participant for update: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	var req struct {
		Name        string   `json:"name"`
		Address     string   `json:"address"`
		AddressName string   `json:"address_name"`
		LabelIDs    *[]int64 `json:"label_ids"`
	}
	var labelIDs []int64
	shouldSetLabels := false

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
		req.AddressName = r.FormValue("address_name")
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
			return
		}
		labelIDs = parsedLabelIDs
		shouldSetLabels = true
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return
		}
		if req.LabelIDs != nil {
			labelIDs = *req.LabelIDs
			shouldSetLabels = true
		}
	}
	req.AddressName = strings.TrimSpace(req.AddressName)

	if message := personLengthMessage(req.Name, req.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageNameAndAddressRequired)
			return
		}
		h.handleValidationError(w, r, messageNameAndAddressRequired)
		return
	}
	if len([]rune(req.AddressName)) > models.MaxAddressNameLength {
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageAddressNameTooLong())
			return
		}
		h.handleValidationError(w, r, messageAddressNameTooLong())
		return
	}
	if shouldSetLabels {
		if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
			log.Printf("[HTTP] PUT /api/v1/participants/{id}: invalid_labels id=%d err=%v", id, err)
			if h.isHTMX(r) {
				h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
				return
			}
			h.handleValidationError(w, r, messageInvalidLabelSelection)
			return
		}
	}

	participant, err := (rosterEditor{db: h.DB, geocoder: h.Geocoder}).updateParticipant(r.Context(), existing, participantEdit{
		Name: req.Name, Address: req.Address, AddressName: req.AddressName, LabelIDs: labelIDs, SetLabels: shouldSetLabels,
	})
	if duplicate, ok := errors.AsType[rosterDuplicateError](err); ok {
		h.handleHTMXErrorNoSwap(w, r, http.StatusConflict, "DUPLICATE_ROSTER_ENTRY", duplicate.Error())
		return
	}
	if geocodeErr, ok := errors.AsType[rosterGeocodeError](err); ok {
		if h.isHTMX(r) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(geocodeErr.err))
			return
		}
		h.handleGeocodingError(w, r, geocodeErr.err)
		return
	}

	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found after update: id=%d", id)
			if h.isHTMX(r) {
				h.handleNotFoundHTMX(w, r, messageParticipantNotFound)
				return
			}
			h.handleNotFound(w, r, messageParticipantNotFound)
			return
		}
		log.Printf("[ERROR] Failed to update participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] Updated participant: id=%d", participant.ID)
	if h.isHTMX(r) {
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list participants after update: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		h.setHTMXToastWithEvent(w, "participantUpdated", messageEntityUpdated("Participant", participant.Name), toastTypeSuccess)
		view, err := h.participantListView(r, participants)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.renderTemplate(w, "participant_list", view)
		return
	}

	response, err := h.participantResponse(r.Context(), participant)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to load participant labels after update: id=%d err=%s", participant.ID, logutil.SafeString(err.Error()))
		h.handleInternalError(w, r, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleDeleteParticipant handles DELETE /api/v1/participants/{id}
func (h *Handler) HandleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] DELETE /api/v1/participants/{id}: invalid_id=%s err=%s", logutil.SafeString(idStr), logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
			return
		}
		h.handleValidationError(w, r, messageInvalidParticipantID)
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/participants/{id}: id=%d", id)
	err = h.DB.Participants().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Participant not found for delete: id=%d", id)
		if h.isHTMX(r) {
			h.handleNotFoundHTMX(w, r, messageParticipantNotFound)
			return
		}
		h.handleNotFound(w, r, messageParticipantNotFound)
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	log.Printf("[HTTP] Deleted participant: id=%d", id)
	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityDeleted("Participant"), toastTypeSuccess)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRestoreParticipant handles POST /api/v1/participants/restore.
func (h *Handler) HandleRestoreParticipant(w http.ResponseWriter, r *http.Request) {
	id, err := parseRestoreID(r)
	if err != nil {
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[HTTP] POST /api/v1/participants/restore: invalid_id err=%s", logutil.SafeString(err.Error()))
		h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
		return
	}

	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] POST /api/v1/participants/restore: id=%d", id)
	if err := h.DB.Participants().Restore(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
			log.Printf("[HTTP] Participant not found for restore: id=%d", id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageParticipantNotFound)
			return
		}
		//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
		log.Printf("[ERROR] Failed to restore participant: id=%d err=%s", id, logutil.SafeString(err.Error()))
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, r, err)
		return
	}

	//nolint:gosec // G706: request-derived values on this log line are parsed numeric IDs or counts.
	log.Printf("[HTTP] Restored participant: id=%d", id)
	if h.isHTMX(r) {
		h.setHTMXToastWithEvent(w, "rosterRestored", messageEntityRestored("Participant"), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleParticipantForm handles GET /api/v1/participants/new and GET /api/v1/participants/{id}/edit
func (h *Handler) HandleParticipantForm(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	idStr = strings.TrimSuffix(idStr, "/edit")

	var participant *models.Participant
	var (
		labels           []models.Label
		selectedLabelIDs map[int64]bool
		err              error
	)
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
			return
		}

		participant, err = h.DB.Participants().GetByID(r.Context(), id)
		if err != nil {
			if h.checkNotFound(err) {
				h.handleNotFoundHTMX(w, r, messageParticipantNotFound)
				return
			}
			h.renderError(w, r, err)
			return
		}
		labels, selectedLabelIDs, err = h.loadLabelsForParticipant(r, participant.ID)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
	} else {
		participant = &models.Participant{}
		labels, err = h.DB.Labels().List(r.Context())
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		selectedLabelIDs = map[int64]bool{}
	}

	h.renderTemplate(w, "participant_form", ParticipantFormView{
		Participant:      participant,
		Labels:           labels,
		SelectedLabelIDs: selectedLabelIDs,
	})
}

func (h *Handler) participantListView(r *http.Request, participants []models.Participant) (ParticipantListView, error) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		return ParticipantListView{}, err
	}
	labelIDs, err := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		return ParticipantListView{}, err
	}
	return ParticipantListView{
		Participants: participants,
		Labels:       labels,
		LabelIDs:     labelIDs,
	}, nil
}

func (h *Handler) loadLabelsForParticipant(r *http.Request, participantID int64) ([]models.Label, map[int64]bool, error) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		return nil, nil, err
	}
	selectedLabels, err := h.DB.Labels().ListLabelsForParticipant(r.Context(), participantID)
	if err != nil {
		return nil, nil, err
	}
	return labels, buildSelectedLabelIDMap(selectedLabels), nil
}

func (h *Handler) participantResponse(ctx context.Context, participant *models.Participant) (ParticipantResponse, error) {
	labels, err := h.DB.Labels().ListLabelsForParticipant(ctx, participant.ID)
	if err != nil {
		return ParticipantResponse{}, err
	}
	labelIDs := make([]int64, 0, len(labels))
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
	}
	return ParticipantResponse{
		Participant: *participant,
		LabelIDs:    labelIDs,
	}, nil
}

func (h *Handler) participantResponses(ctx context.Context, participants []models.Participant) ([]ParticipantResponse, error) {
	labelIDsByParticipant, err := h.DB.Labels().ListLabelIDsForParticipants(ctx)
	if err != nil {
		return nil, err
	}

	responses := make([]ParticipantResponse, 0, len(participants))
	for _, participant := range participants {
		labelIDs := append([]int64{}, labelIDsByParticipant[participant.ID]...)
		if labelIDs == nil {
			labelIDs = []int64{}
		}
		responses = append(responses, ParticipantResponse{
			Participant: participant,
			LabelIDs:    labelIDs,
		})
	}
	return responses, nil
}
