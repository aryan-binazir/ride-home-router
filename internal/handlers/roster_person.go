package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

type rosterRepository[T any] interface {
	GetByID(context.Context, int64) (*T, error)
	List(context.Context, string) ([]T, error)
	ListDeleted(context.Context) ([]T, error)
	Delete(context.Context, int64) error
	Restore(context.Context, int64) error
}

type rosterKind[T, R any] struct {
	name, title, plural                         string
	listTemplate, deletedTemplate, formTemplate string
	createdEvent, updatedEvent                  string
	invalidID, invalidGetID, notFound           string
	hasCapacity                                 bool

	repository func(database.DataStore) rosterRepository[T]
	labels     func(database.LabelRepository, context.Context, int64) ([]models.Label, error)
	labelIDs   func(database.LabelRepository, context.Context) (map[int64][]int64, error)
	identity   func(*T) (id int64, name string)
	create     func(rosterEditor, context.Context, rosterEdit) (*T, error)
	update     func(rosterEditor, context.Context, *T, rosterEdit) (*T, error)
	response   func(*T, []int64) R
	jsonList   func([]R) any
	listView   func(rosterPage[T]) any
	formView   func(*T, []models.Label, map[int64]bool) any
}

func (kind rosterKind[T, R]) list(h *Handler, w http.ResponseWriter, r *http.Request, deleted bool) {
	if h.isHTMX(r) {
		view, err := kind.page(h.DB, r)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		template := kind.listTemplate
		if deleted {
			template = kind.deletedTemplate
		}
		h.renderTemplate(w, template, kind.listView(view))
		return
	}
	response, err := kind.listJSON(h.DB, r)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (kind rosterKind[T, R]) get(h *Handler, w http.ResponseWriter, r *http.Request) {
	idText := strings.TrimPrefix(r.URL.Path, "/api/v1/"+kind.plural+"/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		//nolint:gosec // G706: the ID and parse error are escaped.
		log.Printf("[HTTP] GET /api/v1/%s/{id}: invalid_id=%s err=%s", kind.plural, logutil.SafeString(idText), logutil.SafeString(err.Error()))
		h.handleValidationError(w, r, kind.invalidGetID)
		return
	}

	log.Printf("[HTTP] GET /api/v1/%s/{id}: id=%d", kind.plural, id)
	person, err := kind.repository(h.DB).GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] %s not found: id=%d", kind.title, id)
			h.handleNotFound(w, r, kind.notFound)
			return
		}
		h.handleInternalError(w, r, fmt.Errorf("get %s %d: %w", kind.name, id, err))
		return
	}

	response, err := kind.responseWithLabels(h.DB, r.Context(), person)
	if err != nil {
		personID, _ := kind.identity(person)
		h.handleInternalError(w, r, fmt.Errorf("load %s labels %d: %w", kind.name, personID, err))
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

func (kind rosterKind[T, R]) createPerson(h *Handler, w http.ResponseWriter, r *http.Request) {
	edit, ok := kind.readEdit(h, w, r, true, 0)
	if !ok {
		return
	}

	if kind.hasCapacity {
		log.Printf("[HTTP] POST /api/v1/drivers: capacity=%d", edit.VehicleCapacity)
	} else {
		log.Print("[HTTP] POST /api/v1/participants:")
	}
	person, err := kind.create(rosterEditor{db: h.DB, geocoder: h.Geocoder}, r.Context(), edit)
	if duplicate, ok := errors.AsType[rosterDuplicateError](err); ok {
		h.handleHTMXErrorNoSwap(w, r, http.StatusConflict, "DUPLICATE_ROSTER_ENTRY", duplicate.Error())
		return
	}
	if geocodeErr, ok := errors.AsType[rosterGeocodeError](err); ok {
		log.Printf("[ERROR] Failed to geocode %s address", kind.name)
		h.renderRosterGeocodingError(w, r, geocodeErr.err)
		return
	}
	if err != nil {
		h.renderError(w, r, fmt.Errorf("create %s: %w", kind.name, err))
		return
	}

	id, name := kind.identity(person)
	log.Printf("[HTTP] Created %s: id=%d", kind.name, id)
	if h.isHTMX(r) {
		view, err := kind.page(h.DB, r)
		if err != nil {
			log.Printf("[ERROR] Failed to refresh %s after create: err=%v", kind.plural, err)
			h.setHTMXToastWithEvent(w, kind.createdEvent, kind.title+" saved. Refresh the page to see the updated roster.", toastTypeWarning)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.setHTMXToastWithEvent(w, kind.createdEvent, messageEntityAdded(kind.title, name), toastTypeSuccess)
		h.renderTemplate(w, kind.listTemplate, kind.listView(view))
		return
	}

	labelIDs, _ := uniquePositiveIDs(edit.LabelIDs)
	h.writeJSON(w, http.StatusCreated, kind.response(person, labelIDs))
}

func (kind rosterKind[T, R]) updatePerson(h *Handler, w http.ResponseWriter, r *http.Request) {
	idText := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/"+kind.plural+"/"), "/edit")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		//nolint:gosec // G706: the ID and parse error are escaped.
		log.Printf("[HTTP] PUT /api/v1/%s/{id}: invalid_id=%s err=%s", kind.plural, logutil.SafeString(idText), logutil.SafeString(err.Error()))
		h.handleValidationErrorHTMX(w, r, kind.invalidID)
		return
	}
	log.Printf("[HTTP] PUT /api/v1/%s/{id}: id=%d", kind.plural, id)
	existing, err := kind.repository(h.DB).GetByID(r.Context(), id)
	if err != nil {
		if h.checkNotFound(err) {
			if !kind.hasCapacity {
				log.Printf("[HTTP] Participant not found for update: id=%d", id)
			}
			h.handleNotFoundHTMX(w, r, kind.notFound)
			return
		}
		if !kind.hasCapacity {
			err = fmt.Errorf("get participant for update %d: %w", id, err)
		}
		h.renderError(w, r, err)
		return
	}

	edit, ok := kind.readEdit(h, w, r, false, id)
	if !ok {
		return
	}

	person, err := kind.update(rosterEditor{db: h.DB, geocoder: h.Geocoder}, r.Context(), existing, edit)
	if geocodeErr, ok := errors.AsType[rosterGeocodeError](err); ok {
		h.renderRosterGeocodingError(w, r, geocodeErr.err)
		return
	}
	if err != nil {
		if h.checkNotFound(err) {
			log.Printf("[HTTP] %s not found after update: id=%d", kind.title, id)
			h.handleNotFoundHTMX(w, r, kind.notFound)
			return
		}
		h.renderError(w, r, fmt.Errorf("update %s %d: %w", kind.name, id, err))
		return
	}

	personID, name := kind.identity(person)
	log.Printf("[HTTP] Updated %s: id=%d", kind.name, personID)
	if h.isHTMX(r) {
		view, err := kind.page(h.DB, r)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		h.setHTMXToastWithEvent(w, kind.updatedEvent, messageEntityUpdated(kind.title, name), toastTypeSuccess)
		h.renderTemplate(w, kind.listTemplate, kind.listView(view))
		return
	}

	response, err := kind.responseWithLabels(h.DB, r.Context(), person)
	if err != nil {
		h.handleInternalError(w, r, fmt.Errorf("load %s labels after update %d: %w", kind.name, personID, err))
		return
	}
	h.writeJSON(w, http.StatusOK, response)
}

type rosterPersonBody struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	AddressName string   `json:"address_name"`
	LabelIDs    *[]int64 `json:"label_ids"`
}

func (kind rosterKind[T, R]) readEdit(h *Handler, w http.ResponseWriter, r *http.Request, creating bool, id int64) (rosterEdit, bool) {
	var body struct {
		rosterPersonBody
		VehicleCapacity int `json:"vehicle_capacity"`
	}
	var labelIDs []int64
	setLabels := creating
	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			if creating && !kind.hasCapacity {
				err = fmt.Errorf("parse form: %w", err)
			}
			h.renderError(w, r, err)
			return rosterEdit{}, false
		}
		body.Name = r.FormValue("name")
		body.Address = r.FormValue("address")
		body.AddressName = r.FormValue("address_name")
		if kind.hasCapacity {
			if value := r.FormValue("vehicle_capacity"); value != "" {
				capacity, err := strconv.Atoi(value)
				if err != nil {
					h.handleValidationErrorHTMX(w, r, "Enter a valid vehicle capacity.")
					return rosterEdit{}, false
				}
				body.VehicleCapacity = capacity
			}
		}
		var err error
		labelIDs, err = parseLabelIDs(r)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
			return rosterEdit{}, false
		}
		setLabels = true
	} else {
		var target any = &body.rosterPersonBody
		if kind.hasCapacity {
			target = &body
		}
		if err := httpx.DecodeJSON(r, target); err != nil {
			if creating && !kind.hasCapacity {
				log.Printf("[HTTP] POST /api/v1/participants: invalid_body err=%v", err)
			}
			h.handleValidationError(w, r, messageInvalidRequestBody)
			return rosterEdit{}, false
		}
		if body.LabelIDs != nil {
			labelIDs = *body.LabelIDs
			setLabels = true
		}
	}
	body.AddressName = strings.TrimSpace(body.AddressName)

	if message := personLengthMessage(body.Name, body.Address); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return rosterEdit{}, false
	}
	if body.Name == "" || body.Address == "" {
		if creating && !kind.hasCapacity {
			log.Printf("[HTTP] POST /api/v1/participants: missing_name=%t missing_address=%t", body.Name == "", body.Address == "")
		}
		h.handleValidationErrorHTMX(w, r, messageNameAndAddressRequired)
		return rosterEdit{}, false
	}
	if len([]rune(body.AddressName)) > models.MaxAddressNameLength {
		h.handleValidationErrorHTMX(w, r, messageAddressNameTooLong())
		return rosterEdit{}, false
	}
	if kind.hasCapacity && (body.VehicleCapacity < models.MinVehicleCapacity || body.VehicleCapacity > models.MaxVehicleCapacity) {
		h.handleValidationErrorHTMX(w, r, messageVehicleCapacityOutOfRange())
		return rosterEdit{}, false
	}
	if setLabels {
		if err := h.validateLabelIDs(r.Context(), labelIDs); err != nil {
			if creating {
				log.Printf("[HTTP] POST /api/v1/%s: invalid_labels err=%v", kind.plural, err)
			} else {
				log.Printf("[HTTP] PUT /api/v1/%s/{id}: invalid_labels id=%d err=%v", kind.plural, id, err)
			}
			h.handleValidationErrorHTMX(w, r, messageInvalidLabelSelection)
			return rosterEdit{}, false
		}
	}
	return rosterEdit{
		Name: body.Name, Address: body.Address, AddressName: body.AddressName, LabelIDs: labelIDs, SetLabels: setLabels,
		VehicleCapacity: body.VehicleCapacity,
	}, true
}

func (h *Handler) renderRosterGeocodingError(w http.ResponseWriter, r *http.Request, err error) {
	if h.isHTMX(r) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusUnprocessableEntity, "GEOCODING_FAILED", geocodingErrorMessage(err))
		return
	}
	h.handleGeocodingError(w, r, err)
}

func (kind rosterKind[T, R]) deletePerson(h *Handler, w http.ResponseWriter, r *http.Request) {
	idText := strings.TrimPrefix(r.URL.Path, "/api/v1/"+kind.plural+"/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		//nolint:gosec // G706: the ID and parse error are escaped.
		log.Printf("[HTTP] DELETE /api/v1/%s/{id}: invalid_id=%s err=%s", kind.plural, logutil.SafeString(idText), logutil.SafeString(err.Error()))
		h.handleValidationErrorHTMX(w, r, kind.invalidID)
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/%s/{id}: id=%d", kind.plural, id)
	err = kind.repository(h.DB).Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] %s not found for delete: id=%d", kind.title, id)
		h.handleNotFoundHTMX(w, r, kind.notFound)
		return
	}
	if err != nil {
		h.renderError(w, r, fmt.Errorf("delete %s %d: %w", kind.name, id, err))
		return
	}

	log.Printf("[HTTP] Deleted %s: id=%d", kind.name, id)
	if h.isHTMX(r) {
		h.setHTMXToast(w, messageEntityDeleted(kind.title), toastTypeSuccess)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (kind rosterKind[T, R]) restorePerson(h *Handler, w http.ResponseWriter, r *http.Request) {
	id, err := parseRestoreID(r)
	if err != nil {
		//nolint:gosec // G706: the parse error is escaped.
		log.Printf("[HTTP] POST /api/v1/%s/restore: invalid_id err=%s", kind.plural, logutil.SafeString(err.Error()))
		h.handleValidationErrorHTMX(w, r, kind.invalidID)
		return
	}

	//nolint:gosec // G706: the request-derived ID is an integer.
	log.Printf("[HTTP] POST /api/v1/%s/restore: id=%d", kind.plural, id)
	if err := kind.repository(h.DB).Restore(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			//nolint:gosec // G706: the request-derived ID is an integer.
			log.Printf("[HTTP] %s not found for restore: id=%d", kind.title, id)
			h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", kind.notFound)
			return
		}
		h.renderError(w, r, fmt.Errorf("restore %s %d: %w", kind.name, id, err))
		return
	}

	//nolint:gosec // G706: the request-derived ID is an integer.
	log.Printf("[HTTP] Restored %s: id=%d", kind.name, id)
	if h.isHTMX(r) {
		h.setHTMXToastWithEvent(w, "rosterRestored", messageEntityRestored(kind.title), toastTypeSuccess)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (kind rosterKind[T, R]) form(h *Handler, w http.ResponseWriter, r *http.Request) {
	idText := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/"+kind.plural+"/"), "/edit")
	editing := idText != "new" && idText != ""
	person := new(T)
	if editing {
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, kind.invalidID)
			return
		}

		person, err = kind.repository(h.DB).GetByID(r.Context(), id)
		if err != nil {
			if h.checkNotFound(err) {
				h.handleNotFoundHTMX(w, r, kind.notFound)
				return
			}
			h.renderError(w, r, err)
			return
		}
	}

	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}
	selected := map[int64]bool{}
	if editing {
		id, _ := kind.identity(person)
		memberships, err := kind.labels(h.DB.Labels(), r.Context(), id)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		selected = buildSelectedLabelIDMap(memberships)
	}
	h.renderTemplate(w, kind.formTemplate, kind.formView(person, labels, selected))
}

func (kind rosterKind[T, R]) responseWithLabels(db database.DataStore, ctx context.Context, person *T) (R, error) {
	id, _ := kind.identity(person)
	labels, err := kind.labels(db.Labels(), ctx, id)
	if err != nil {
		var empty R
		return empty, err
	}
	labelIDs := make([]int64, 0, len(labels))
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
	}
	return kind.response(person, labelIDs), nil
}
