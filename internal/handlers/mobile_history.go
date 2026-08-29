package handlers

import (
	"net/http"
)

func (h *Handler) HandleMobileHistory(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	view, err := h.buildEventListView(r.Context(), 100, 0)
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	groups := make([]mobileHistoryGroup, 0)
	for _, event := range view.Events {
		label := event.EventDate.Format("January 2006")
		if len(groups) == 0 || groups[len(groups)-1].Label != label {
			groups = append(groups, mobileHistoryGroup{Label: label})
		}
		groups[len(groups)-1].Events = append(groups[len(groups)-1].Events, event)
	}
	h.renderTemplate(w, "mobile/history.html", mobileHistoryView{mobileBaseView: newMobileBase("History", "history", ""), Groups: groups, UseMiles: view.UseMiles})
}

func (h *Handler) HandleMobileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, err := mobileID(r.URL.Path, "/m/history/", "")
	if err != nil {
		h.renderMobileError(w, r, http.StatusNotFound, messageEventNotFound, err)
		return
	}
	event, routes, summary, err := h.DB.Events().GetByID(r.Context(), id)
	if err != nil {
		h.renderMobileStoreError(w, r, err, messageEventNotFound)
		return
	}
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return
	}
	savedRoutes := make([]mobileSavedRoute, 0, len(routes))
	for _, route := range routes {
		savedRoutes = append(savedRoutes, mobileSavedRoute{Route: route, DriverText: formatSavedMobileHandoff(route, false), ParentText: formatSavedMobileHandoff(route, true)})
	}
	h.renderTemplate(w, "mobile/history_detail.html", mobileHistoryDetailView{mobileBaseView: newMobileBase("Saved event", "history", ""), Event: event, Routes: savedRoutes, Summary: summary, UseMiles: settings != nil && settings.UseMiles})
}
