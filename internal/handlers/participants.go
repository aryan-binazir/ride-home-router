package handlers

import (
	"context"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type ParticipantListResponse struct {
	Participants []ParticipantResponse `json:"participants"`
	Total        int                   `json:"total"`
}

type ParticipantResponse struct {
	models.Participant
	LabelIDs []int64 `json:"label_ids"`
}

var participantRoster = rosterKind[models.Participant, ParticipantResponse]{
	name: "participant", title: "Participant", plural: "participants",
	listTemplate: "participant_list", deletedTemplate: "participant_deleted_list", formTemplate: "participant_form",
	createdEvent: "participantCreated", updatedEvent: "participantUpdated",
	invalidID: messageInvalidParticipantID, invalidGetID: "Choose a valid rider. Refresh the page and try again.", notFound: messageParticipantNotFound,

	repository: func(db database.DataStore) rosterRepository[models.Participant] { return db.Participants() },
	labels:     database.LabelRepository.ListLabelsForParticipant,
	labelIDs:   database.LabelRepository.ListLabelIDsForParticipants,
	identity:   func(p *models.Participant) (int64, string) { return p.ID, p.Name },
	create: func(e rosterEditor, ctx context.Context, edit driverEdit) (*models.Participant, error) {
		return e.createParticipant(ctx, edit.participantEdit)
	},
	update: func(e rosterEditor, ctx context.Context, p *models.Participant, edit driverEdit) (*models.Participant, error) {
		return e.updateParticipant(ctx, p, edit.participantEdit)
	},
	response: func(p *models.Participant, ids []int64) ParticipantResponse {
		return ParticipantResponse{Participant: *p, LabelIDs: ids}
	},
	jsonList: func(people []ParticipantResponse) any {
		return ParticipantListResponse{Participants: people, Total: len(people)}
	},
	listView: func(page rosterPage[models.Participant]) any { return participantListView(page) },
	formView: func(person *models.Participant, labels []models.Label, selected map[int64]bool) any {
		return ParticipantFormView{Participant: person, Labels: labels, SelectedLabelIDs: selected}
	},
}

func (h *Handler) HandleListParticipants(w http.ResponseWriter, r *http.Request) {
	participantRoster.list(h, w, r, false)
}

func (h *Handler) HandleListDeletedParticipants(w http.ResponseWriter, r *http.Request) {
	participantRoster.list(h, w, r, true)
}

func (h *Handler) HandleGetParticipant(w http.ResponseWriter, r *http.Request) {
	participantRoster.get(h, w, r)
}

func (h *Handler) HandleCreateParticipant(w http.ResponseWriter, r *http.Request) {
	participantRoster.createPerson(h, w, r)
}

func (h *Handler) HandleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
	participantRoster.updatePerson(h, w, r)
}

func (h *Handler) HandleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	participantRoster.deletePerson(h, w, r)
}

func (h *Handler) HandleRestoreParticipant(w http.ResponseWriter, r *http.Request) {
	participantRoster.restorePerson(h, w, r)
}

func (h *Handler) HandleParticipantForm(w http.ResponseWriter, r *http.Request) {
	participantRoster.form(h, w, r)
}

func (h *Handler) participantResponse(ctx context.Context, person *models.Participant) (ParticipantResponse, error) {
	return participantRoster.responseWithLabels(h.DB, ctx, person)
}
