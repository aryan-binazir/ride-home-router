package handlers

import (
	"context"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type DriverListResponse struct {
	Drivers []DriverResponse `json:"drivers"`
	Total   int              `json:"total"`
}

type DriverResponse struct {
	models.Driver
	LabelIDs []int64 `json:"label_ids"`
}

var driverRoster = rosterKind[models.Driver, DriverResponse]{
	name: "driver", title: "Driver", plural: "drivers",
	listTemplate: "driver_list", deletedTemplate: "driver_deleted_list", formTemplate: "driver_form",
	createdEvent: "driverCreated", updatedEvent: "driverUpdated",
	invalidID: messageInvalidDriverID, invalidGetID: messageInvalidDriverID, notFound: messageDriverNotFound,
	hasCapacity: true,

	repository: func(db database.DataStore) rosterRepository[models.Driver] { return db.Drivers() },
	labels:     database.LabelRepository.ListLabelsForDriver,
	labelIDs:   database.LabelRepository.ListLabelIDsForDrivers,
	identity:   func(p *models.Driver) (int64, string) { return p.ID, p.Name },
	create:     rosterEditor.createDriver,
	update:     rosterEditor.updateDriver,
	response: func(p *models.Driver, ids []int64) DriverResponse {
		return DriverResponse{Driver: *p, LabelIDs: ids}
	},
	jsonList: func(people []DriverResponse) any {
		return DriverListResponse{Drivers: people, Total: len(people)}
	},
	listView: func(page rosterPage[models.Driver]) any { return driverListView(page) },
	formView: func(person *models.Driver, labels []models.Label, selected map[int64]bool) any {
		return DriverFormView{Driver: person, Labels: labels, SelectedLabelIDs: selected}
	},
}

func (h *Handler) HandleListDrivers(w http.ResponseWriter, r *http.Request) {
	driverRoster.list(h, w, r, false)
}

func (h *Handler) HandleListDeletedDrivers(w http.ResponseWriter, r *http.Request) {
	driverRoster.list(h, w, r, true)
}

func (h *Handler) HandleGetDriver(w http.ResponseWriter, r *http.Request) {
	driverRoster.get(h, w, r)
}

func (h *Handler) HandleCreateDriver(w http.ResponseWriter, r *http.Request) {
	driverRoster.createPerson(h, w, r)
}

func (h *Handler) HandleUpdateDriver(w http.ResponseWriter, r *http.Request) {
	driverRoster.updatePerson(h, w, r)
}

func (h *Handler) HandleDeleteDriver(w http.ResponseWriter, r *http.Request) {
	driverRoster.deletePerson(h, w, r)
}

func (h *Handler) HandleRestoreDriver(w http.ResponseWriter, r *http.Request) {
	driverRoster.restorePerson(h, w, r)
}

func (h *Handler) HandleDriverForm(w http.ResponseWriter, r *http.Request) {
	driverRoster.form(h, w, r)
}

func (h *Handler) driverResponse(ctx context.Context, person *models.Driver) (DriverResponse, error) {
	return driverRoster.responseWithLabels(h.DB, ctx, person)
}
