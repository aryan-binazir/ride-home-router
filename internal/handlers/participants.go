package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

// ParticipantListResponse represents the list response
type ParticipantListResponse struct {
	Participants []models.Participant `json:"participants"`
	Total        int                  `json:"total"`
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

	h.writeJSON(w, http.StatusOK, ParticipantListResponse{
		Participants: participants,
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
		log.Printf("[ERROR] Failed to get participant: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		log.Printf("[HTTP] Participant not found: id=%d", id)
		h.handleNotFound(w, "Participant not found")
		return
	}

	h.writeJSON(w, http.StatusOK, participant)
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
			h.renderError(w, r, errors.New("Invalid label selection"))
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

	participant, err = h.DB.Participants().Create(r.Context(), participant)
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
		if err := h.DB.Labels().SetLabelsForParticipant(r.Context(), participant.ID, labelIDs); err != nil {
			log.Printf("[ERROR] Failed to set participant labels: id=%d err=%v", participant.ID, err)
			h.renderError(w, r, err)
			return
		}
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
	if err := h.DB.Labels().SetLabelsForParticipant(r.Context(), participant.ID, labelIDs); err != nil {
		log.Printf("[ERROR] Failed to set participant labels: id=%d err=%v", participant.ID, err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, participant)
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
		log.Printf("[ERROR] Failed to get participant for update: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		log.Printf("[HTTP] Participant not found for update: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageParticipantNotFound))
			return
		}
		h.handleNotFound(w, messageParticipantNotFound)
		return
	}

	var req struct {
		Name     string  `json:"name"`
		Address  string  `json:"address"`
		LabelIDs []int64 `json:"label_ids"`
	}
	var labelIDs []int64

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
		parsedLabelIDs, err := parseLabelIDs(r)
		if err != nil {
			h.renderError(w, r, errors.New("Invalid label selection"))
			return
		}
		labelIDs = parsedLabelIDs
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		labelIDs = req.LabelIDs
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageNameAndAddressRequired))
			return
		}
		h.handleValidationError(w, messageNameAndAddressRequired)
		return
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

	participant, err = h.DB.Participants().Update(r.Context(), participant)
	if err != nil {
		log.Printf("[ERROR] Failed to update participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		log.Printf("[HTTP] Participant not found after update: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, errors.New(messageParticipantNotFound))
			return
		}
		h.handleNotFound(w, messageParticipantNotFound)
		return
	}

	log.Printf("[HTTP] Updated participant: id=%d name=%s", participant.ID, participant.Name)
	if err := h.DB.Labels().SetLabelsForParticipant(r.Context(), participant.ID, labelIDs); err != nil {
		log.Printf("[ERROR] Failed to set participant labels: id=%d err=%v", participant.ID, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}
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

	h.writeJSON(w, http.StatusOK, participant)
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
			h.renderError(w, r, err)
			return
		}
		if participant == nil {
			h.renderError(w, r, errors.New(messageParticipantNotFound))
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
