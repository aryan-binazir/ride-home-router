package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"unicode/utf8"
)

type mobileFormError struct{ message string }

func (e mobileFormError) Error() string { return e.message }

func (h *Handler) HandleMobilePeople(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	drivers, err := h.DB.Drivers().List(r.Context(), search)
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	participantLabels, err := h.DB.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	driverLabels, err := h.DB.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	h.renderTemplate(w, "mobile/people.html", mobilePeopleView{mobileBaseView: newMobileBase("People", "people", ""), Participants: participants, Drivers: drivers, Labels: labels, ParticipantLabels: participantLabels, DriverLabels: driverLabels})
}

func (h *Handler) HandleMobileParticipantForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePersonForm(w, r, "participant")
}

func (h *Handler) HandleMobileDriverForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePersonForm(w, r, "driver")
}

func (h *Handler) mobilePersonForm(w http.ResponseWriter, r *http.Request, kind string) {
	logMobileRequest(r)
	prefix := "/m/people/" + kind + "s/"
	isNew := strings.HasSuffix(r.URL.Path, "/new")
	var id int64
	var err error
	if !isNew {
		id, err = mobileID(r.URL.Path, prefix, "/edit")
		if err != nil {
			h.renderMobileError(w, r, http.StatusNotFound, "Page not found. Return to your plan and try again.", err)
			return
		}
	}
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		if r.Method == http.MethodPost {
			h.renderMobileTemplateStatus(w, r, http.StatusInternalServerError, "mobile/person_form.html", mobilePersonSubmittedView(r, kind, nil, messageGenericInternalError))
			return
		}
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	if r.Method == http.MethodPost {
		if err := h.saveMobilePerson(r, kind, id); err != nil {
			if geocodeErr, ok := errors.AsType[rosterGeocodeError](err); ok {
				log.Print("[ERROR] Mobile geocoding failed")
				err = mobileFormError{geocodingErrorMessage(geocodeErr.err)}
			}
			if h.checkNotFound(err) {
				h.renderMobileError(w, r, http.StatusNotFound, mobilePersonNotFoundMessage(kind), err)
				return
			}
			if duplicate, ok := errors.AsType[rosterDuplicateError](err); ok {
				h.renderMobileTemplateStatus(w, r, http.StatusConflict, "mobile/person_form.html", mobilePersonSubmittedView(r, kind, labels, duplicate.Error()))
				return
			}
			if formErr, ok := errors.AsType[mobileFormError](err); ok {
				view := mobilePersonSubmittedView(r, kind, labels, formErr.message)
				h.renderMobileTemplateStatus(w, r, http.StatusBadRequest, "mobile/person_form.html", view)
				return
			}
			log.Print("[ERROR] Mobile save failed")
			h.renderMobileTemplateStatus(w, r, http.StatusInternalServerError, "mobile/person_form.html", mobilePersonSubmittedView(r, kind, labels, messageGenericInternalError))
			return
		}
		//nolint:gosec // mobileReturnPath permits only local /m/ paths without a scheme, host, or backslash.
		http.Redirect(w, r, mobileReturnPath(r, "/m/people"), http.StatusSeeOther)
		return
	}
	view := mobilePersonFormView{mobileBaseView: newMobileBase(mobilePersonTitle(kind, isNew), "people", ""), Kind: kind, Action: mobileFormAction(r), Labels: labels, Selected: map[int64]bool{}}
	if isNew && kind == "driver" {
		view.VehicleCapacity = 4
	}
	if !isNew && kind == "participant" {
		person, getErr := h.DB.Participants().GetByID(r.Context(), id)
		if getErr != nil {
			h.renderMobileStoreError(w, r, getErr, messageParticipantNotFound)
			return
		}
		view.Name, view.Address, view.AddressName = person.Name, person.Address, person.AddressName
		personLabels, labelsErr := h.DB.Labels().ListLabelsForParticipant(r.Context(), id)
		if labelsErr != nil {
			h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, labelsErr)
			return
		}
		for _, label := range personLabels {
			view.Selected[label.ID] = true
		}
	}
	if !isNew && kind == "driver" {
		person, getErr := h.DB.Drivers().GetByID(r.Context(), id)
		if getErr != nil {
			h.renderMobileStoreError(w, r, getErr, messageDriverNotFound)
			return
		}
		view.Name, view.Address, view.AddressName, view.VehicleCapacity = person.Name, person.Address, person.AddressName, person.VehicleCapacity
		personLabels, labelsErr := h.DB.Labels().ListLabelsForDriver(r.Context(), id)
		if labelsErr != nil {
			h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, labelsErr)
			return
		}
		for _, label := range personLabels {
			view.Selected[label.ID] = true
		}
	}
	h.renderTemplate(w, "mobile/person_form.html", view)
}

func mobilePersonSubmittedView(r *http.Request, kind string, labels []models.Label, message string) mobilePersonFormView {
	capacity, _ := strconv.Atoi(r.FormValue("vehicle_capacity"))
	return mobilePersonFormView{
		mobileBaseView: newMobileBase(mobilePersonTitle(kind, strings.HasSuffix(r.URL.Path, "/new")), "people", message),
		Kind:           kind, Action: mobileFormAction(r), Name: r.FormValue("name"), Address: r.FormValue("address"),
		AddressName: r.FormValue("address_name"), VehicleCapacity: capacity, Labels: labels,
		Selected: mobileSelected(parseMobileIDs(r.Form["label_ids"])),
	}
}

func mobilePersonTitle(kind string, isNew bool) string {
	verb := "Edit"
	if isNew {
		verb = "Add"
	}
	return verb + " " + kind
}

func mobilePersonNotFoundMessage(kind string) string {
	if kind == "driver" {
		return messageDriverNotFound
	}
	return messageParticipantNotFound
}

func (h *Handler) saveMobilePerson(r *http.Request, kind string, id int64) error {
	if err := r.ParseForm(); err != nil {
		return mobileFormError{messageMobileInvalidForm}
	}
	name := strings.TrimSpace(r.FormValue("name"))
	address := strings.TrimSpace(r.FormValue("address"))
	addressName := strings.TrimSpace(r.FormValue("address_name"))
	if message := personLengthMessage(name, address); message != "" {
		return mobileFormError{message}
	}
	if name == "" || address == "" {
		return mobileFormError{messageNameAndAddressRequired}
	}
	if utf8.RuneCountInString(addressName) > models.MaxAddressNameLength {
		return mobileFormError{messageAddressNameTooLong()}
	}
	labels, err := parseLabelIDs(r)
	if err != nil {
		return mobileFormError{messageInvalidLabelSelection}
	}
	if err := h.validateLabelIDs(r.Context(), labels); err != nil {
		return mobileFormError{messageInvalidLabelSelection}
	}
	//nolint:gosec // G706: every request-derived string on this log line is escaped with logutil.SafeString.
	log.Printf("[HTTP] Mobile save person: kind=%s id=%d", logutil.SafeString(kind), id)
	editor := rosterEditor{db: h.DB, geocoder: h.Geocoder}
	edit := participantEdit{Name: name, Address: address, AddressName: addressName, LabelIDs: labels, SetLabels: true}
	if kind == "participant" {
		if id > 0 {
			existing, err := h.DB.Participants().GetByID(r.Context(), id)
			if err != nil {
				return err
			}
			_, err = editor.updateParticipant(r.Context(), existing, edit)
			return err
		}
		_, err := editor.createParticipant(r.Context(), edit)
		return err
	}
	capacity, err := strconv.Atoi(r.FormValue("vehicle_capacity"))
	if err != nil || capacity < models.MinVehicleCapacity || capacity > models.MaxVehicleCapacity {
		return mobileFormError{messageVehicleCapacityOutOfRange()}
	}
	driver := driverEdit{participantEdit: edit, VehicleCapacity: capacity}
	if id > 0 {
		existing, err := h.DB.Drivers().GetByID(r.Context(), id)
		if err != nil {
			return err
		}
		_, err = editor.updateDriver(r.Context(), existing, driver)
		return err
	}
	_, err = editor.createDriver(r.Context(), driver)
	return err
}

func (h *Handler) geocodeMobile(ctx context.Context, address string, lat, lng *float64) error {
	result, err := h.Geocoder.GeocodeWithRetry(ctx, address, 3)
	if err != nil {
		log.Print("[ERROR] Mobile geocoding failed")
		return mobileFormError{geocodingErrorMessage(err)}
	}
	*lat, *lng = result.Coords.Lat, result.Coords.Lng
	return nil
}
