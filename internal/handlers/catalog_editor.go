package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type catalogEditorView struct {
	Sync                                                                             string
	Title, URL, SubmitURL, Target, Include, ChoiceName, Search, PreviousURL, NextURL string
	Choices                                                                          []editorChoice
}

func renderCatalogPage(h *Handler, w http.ResponseWriter, r *http.Request, view catalogEditorView, choices []editorChoice) {
	view.Search = strings.TrimSpace(r.FormValue("search"))
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	offset = min(len(choices), max(0, offset))
	end := min(len(choices), offset+editorPageSize)
	view.Choices = choices[offset:end]
	pageURL := func(start int) string {
		u, _ := url.Parse(view.URL)
		q := u.Query()
		q.Set("search", view.Search)
		q.Set("offset", strconv.Itoa(start))
		u.RawQuery = q.Encode()
		return u.String()
	}
	if offset > 0 {
		view.PreviousURL = pageURL(max(0, offset-editorPageSize))
	}
	if end < len(choices) {
		view.NextURL = pageURL(end)
	}
	h.renderTemplate(w, "catalog_editor", view)
}

func (h *Handler) HandleLocationEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		id, err := strconv.ParseInt(r.FormValue("location_id"), 10, 64)
		if err != nil || id <= 0 {
			h.handleValidationErrorHTMX(w, r, messageChooseValidActivityLocation)
			return
		}
		location, err := h.DB.ActivityLocations().GetByID(r.Context(), id)
		if err != nil {
			if h.checkNotFound(err) {
				h.handleNotFoundHTMX(w, r, "Location no longer available")
			} else {
				h.handleInternalError(w, r, err)
			}
			return
		}
		w.Header().Set("HX-Retarget", "#event-activity-location-select")
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderTemplate(w, "location_assignment", location)
		return
	}
	locations, err := h.DB.ActivityLocations().List(r.Context())
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	search := strings.ToLower(strings.TrimSpace(r.FormValue("search")))
	choices := make([]editorChoice, 0, len(locations))
	for _, location := range locations {
		if strings.Contains(strings.ToLower(location.Name+" "+location.Address), search) {
			choices = append(choices, editorChoice{Value: location.ID, Name: location.Name, Detail: location.Address})
		}
	}
	renderCatalogPage(h, w, r, catalogEditorView{Title: "Choose location", URL: "/api/v1/planner/location-editor", SubmitURL: "/api/v1/planner/location-editor", Target: "#event-activity-location-select", ChoiceName: "location_id"}, choices)
}

func (h *Handler) HandleBulkLabelEditor(w http.ResponseWriter, r *http.Request) {
	kind, action := r.FormValue("kind"), r.FormValue("action")
	if (kind != "participants" && kind != "drivers") || (action != "add" && action != "remove") {
		h.handleNotFoundHTMX(w, r, "Label action not found")
		return
	}
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	search := strings.ToLower(strings.TrimSpace(r.FormValue("search")))
	choices := make([]editorChoice, 0, len(labels))
	for _, label := range labels {
		if strings.Contains(strings.ToLower(label.Name), search) {
			choices = append(choices, editorChoice{Value: label.ID, Name: label.Name})
		}
	}
	singular := "participant"
	if kind == "drivers" {
		singular = "driver"
	}
	title := "Add label"
	if action == "remove" {
		title = "Remove label"
	}
	renderCatalogPage(h, w, r, catalogEditorView{Sync: "#" + singular + "-bulk-form:queue last", Title: title, URL: "/api/v1/planner/label-editor?kind=" + kind + "&action=" + action, SubmitURL: "/api/v1/" + kind + "/labels/" + action, Target: "#" + kind + "-list", Include: "#" + singular + "-bulk-form input[name='" + singular + "_ids']:checked", ChoiceName: "label_id"}, choices)
}
