package handlers

import (
	"errors"
	"log"
	"net/http"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

func (h *Handler) HandleMobilePlaces(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	vans, err := h.DB.OrganizationVehicles().List(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	h.renderTemplate(w, "mobile/places.html", mobilePlacesView{mobileBaseView: newMobileBase("Places", "places", ""), Locations: locations, Vans: vans})
}

func (h *Handler) HandleMobileLocationForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePlaceForm(w, r, "location")
}

func (h *Handler) HandleMobileVanForm(w http.ResponseWriter, r *http.Request) {
	h.mobilePlaceForm(w, r, "van")
}

func (h *Handler) mobilePlaceForm(w http.ResponseWriter, r *http.Request, kind string) {
	logMobileRequest(r)
	prefix := "/m/places/"
	if kind == "location" {
		prefix += "locations/"
	} else {
		prefix += "vans/"
	}
	isNew := strings.HasSuffix(r.URL.Path, "/new")
	var id int64
	var err error
	if !isNew {
		id, err = mobileID(r.URL.Path, prefix, "/edit")
		if err != nil {
			h.renderMobileError(w, r, http.StatusNotFound, "Page not found", err)
			return
		}
	}
	if r.Method == http.MethodPost {
		if err := h.saveMobilePlace(r, kind, id); err != nil {
			if h.checkNotFound(err) {
				h.renderMobileError(w, r, http.StatusNotFound, mobilePlaceNotFoundMessage(kind), err)
				return
			}
			if formErr, ok := errors.AsType[mobileFormError](err); ok {
				view := mobilePlaceSubmittedView(r, kind, formErr.message)
				h.renderMobileTemplateStatus(w, r, http.StatusBadRequest, "mobile/place_form.html", view)
				return
			}
			h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
			return
		}
		http.Redirect(w, r, "/m/places", http.StatusSeeOther)
		return
	}
	view := mobilePlaceFormView{mobileBaseView: newMobileBase(mobilePlaceTitle(kind, isNew), "places", ""), Kind: kind, Action: r.URL.Path}
	if isNew && kind == "van" {
		view.Capacity = 8
	}
	if !isNew && kind == "location" {
		place, getErr := h.DB.ActivityLocations().GetByID(r.Context(), id)
		if getErr != nil {
			h.renderMobileStoreError(w, r, getErr, messageSelectedActivityLocationNotFound)
			return
		}
		view.Name, view.Address = place.Name, place.Address
	}
	if !isNew && kind == "van" {
		place, getErr := h.DB.OrganizationVehicles().GetByID(r.Context(), id)
		if getErr != nil {
			h.renderMobileStoreError(w, r, getErr, messageOrganizationVehicleNotFound)
			return
		}
		view.Name, view.Capacity = place.Name, place.Capacity
	}
	h.renderTemplate(w, "mobile/place_form.html", view)
}

func mobilePlaceSubmittedView(r *http.Request, kind, message string) mobilePlaceFormView {
	capacity, _ := strconv.Atoi(r.FormValue("capacity"))
	return mobilePlaceFormView{mobileBaseView: newMobileBase(mobilePlaceTitle(kind, strings.HasSuffix(r.URL.Path, "/new")), "places", message), Kind: kind, Action: r.URL.Path, Name: r.FormValue("name"), Address: r.FormValue("address"), Capacity: capacity}
}

func mobilePlaceTitle(kind string, isNew bool) string {
	verb := "Edit"
	if isNew {
		verb = "Add"
	}
	return verb + " " + kind
}

func mobilePlaceNotFoundMessage(kind string) string {
	if kind == "van" {
		return messageOrganizationVehicleNotFound
	}
	return messageSelectedActivityLocationNotFound
}

func (h *Handler) saveMobilePlace(r *http.Request, kind string, id int64) error {
	if err := r.ParseForm(); err != nil {
		return mobileFormError{messageMobileInvalidForm}
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return mobileFormError{messageNameRequired}
	}
	//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
	log.Printf("[HTTP] Mobile save place: kind=%s id=%d name=%s", logutil.SafeString(kind), id, logutil.SafeString(name))
	if kind == "van" {
		capacity, err := strconv.Atoi(r.FormValue("capacity"))
		if err != nil || capacity < 1 {
			return mobileFormError{messageOrganizationVehicleCapacityMustBeAtLeastOne}
		}
		vehicle := &models.OrganizationVehicle{ID: id, Name: name, Capacity: capacity}
		if id > 0 {
			_, err = h.DB.OrganizationVehicles().Update(r.Context(), vehicle)
		} else {
			_, err = h.DB.OrganizationVehicles().Create(r.Context(), vehicle)
		}
		return err
	}
	address := strings.TrimSpace(r.FormValue("address"))
	if address == "" {
		return mobileFormError{messageAddressRequired}
	}
	location := &models.ActivityLocation{ID: id, Name: name, Address: address}
	if id > 0 {
		existing, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
		if err != nil {
			return err
		}
		location.Lat, location.Lng = existing.Lat, existing.Lng
		if existing.Address != address {
			if err := h.geocodeMobile(r.Context(), address, &location.Lat, &location.Lng); err != nil {
				return err
			}
		}
		_, err = h.DB.ActivityLocations().Update(r.Context(), location)
		return err
	}
	if err := h.geocodeMobile(r.Context(), address, &location.Lat, &location.Lng); err != nil {
		return err
	}
	_, err := h.DB.ActivityLocations().Create(r.Context(), location)
	return err
}
