package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
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
	log.Printf("[HTTP] GET /api/v1/participants: search=%s", search)

	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		log.Printf("[ERROR] Failed to list participants: search=%s err=%v", search, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

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
		h.handleInternalError(w, err)
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
		log.Printf("[HTTP] GET /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/participants/{id}: id=%d", id)
	participant, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found: id=%d", id)
			h.handleNotFound(w, "Participant not found")
			return
		}
		log.Printf("[ERROR] Failed to get participant: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	response, err := h.participantResponse(r.Context(), participant)
	if err != nil {
		log.Printf("[ERROR] Failed to load participant labels: id=%d err=%v", participant.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleCreateParticipant handles POST /api/v1/participants
func (h *Handler) HandleCreateParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string  `json:"name"`
		Address  string  `json:"address"`
		LabelIDs []int64 `json:"label_ids"`
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
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.renderError(w, r, errors.New("invalid label selection"))
			return
		}
		labelIDs = parsedLabelIDs
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/participants: invalid_body err=%v", err)
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		labelIDs = req.LabelIDs
	}

	if req.Name == "" || req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/participants: missing_fields name=%s address=%s", req.Name, req.Address)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageNameAndAddressRequired))
			return
		}
		h.handleValidationError(w, messageNameAndAddressRequired)
		return
	}
	if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
		log.Printf("[HTTP] POST /api/v1/participants: invalid_labels err=%v", err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidLabelSelection))
			return
		}
		h.handleValidationError(w, messageInvalidLabelSelection)
		return
	}

	log.Printf("[HTTP] POST /api/v1/participants: name=%s address=%s", req.Name, req.Address)
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode participant address: address=%s err=%v", req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleGeocodingError(w, err)
		return
	}

	participant := &models.Participant{
		Name:    req.Name,
		Address: req.Address,
		Lat:     geocodeResult.Coords.Lat,
		Lng:     geocodeResult.Coords.Lng,
	}

	participant, err = h.DB.Participants().CreateWithLabels(r.Context(), participant, labelIDs)
	if err != nil {
		log.Printf("[ERROR] Failed to create participant: name=%s address=%s err=%v", req.Name, req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created participant: id=%d name=%s", participant.ID, participant.Name)
	if h.isHTMX(r) {
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list participants after create: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		h.setHTMXToastWithEvent(w, "participantCreated", messageEntityAdded("Participant", participant.Name), toastTypeSuccess)
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
		log.Printf("[ERROR] Failed to load participant labels after create: id=%d err=%v", participant.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, response)
}

// HandleUpdateParticipant handles PUT /api/v1/participants/{id}
func (h *Handler) HandleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	if trimmedID, ok := strings.CutSuffix(idStr, "/edit"); ok {
		idStr = trimmedID
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidParticipantID))
			return
		}
		h.handleValidationError(w, messageInvalidParticipantID)
		return
	}

	log.Printf("[HTTP] PUT /api/v1/participants/{id}: id=%d", id)

	existing, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found for update: id=%d", id)
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageParticipantNotFound))
				return
			}
			h.handleNotFound(w, messageParticipantNotFound)
			return
		}
		log.Printf("[ERROR] Failed to get participant for update: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	var req struct {
		Name     string   `json:"name"`
		Address  string   `json:"address"`
		LabelIDs *[]int64 `json:"label_ids"`
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
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.renderError(w, r, errors.New("invalid label selection"))
			return
		}
		labelIDs = parsedLabelIDs
		shouldSetLabels = true
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		if req.LabelIDs != nil {
			labelIDs = *req.LabelIDs
			shouldSetLabels = true
		}
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageNameAndAddressRequired))
			return
		}
		h.handleValidationError(w, messageNameAndAddressRequired)
		return
	}
	if shouldSetLabels {
		if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
			log.Printf("[HTTP] PUT /api/v1/participants/{id}: invalid_labels id=%d err=%v", id, err)
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageInvalidLabelSelection))
				return
			}
			h.handleValidationError(w, messageInvalidLabelSelection)
			return
		}
	}

	participant := &models.Participant{
		ID:        id,
		Name:      req.Name,
		Address:   req.Address,
		Lat:       existing.Lat,
		Lng:       existing.Lng,
		CreatedAt: existing.CreatedAt,
	}

	if req.Address != existing.Address {
		geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
		if err != nil {
			if h.isHTMX(r) {
				h.renderError(w, r, err)
				return
			}
			h.handleGeocodingError(w, err)
			return
		}
		participant.Lat = geocodeResult.Coords.Lat
		participant.Lng = geocodeResult.Coords.Lng
	}

	if shouldSetLabels {
		participant, err = h.DB.Participants().UpdateWithLabels(r.Context(), participant, labelIDs)
	} else {
		participant, err = h.DB.Participants().Update(r.Context(), participant)
	}
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] Participant not found after update: id=%d", id)
			if h.isHTMX(r) {
				h.renderError(w, r, errors.New(messageParticipantNotFound))
				return
			}
			h.handleNotFound(w, messageParticipantNotFound)
			return
		}
		log.Printf("[ERROR] Failed to update participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated participant: id=%d name=%s", participant.ID, participant.Name)
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
		log.Printf("[ERROR] Failed to load participant labels after update: id=%d err=%v", participant.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleDeleteParticipant handles DELETE /api/v1/participants/{id}
func (h *Handler) HandleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageInvalidParticipantID))
			return
		}
		h.handleValidationError(w, messageInvalidParticipantID)
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/participants/{id}: id=%d", id)
	err = h.DB.Participants().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Participant not found for delete: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageParticipantNotFound))
			return
		}
		h.handleNotFound(w, messageParticipantNotFound)
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
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
			h.renderError(w, r, errors.New(messageInvalidParticipantID))
			return
		}

		participant, err = h.DB.Participants().GetByID(r.Context(), id)
		if err != nil {
			if h.checkNotFound(err) {
				h.renderError(w, r, errors.New(messageParticipantNotFound))
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
