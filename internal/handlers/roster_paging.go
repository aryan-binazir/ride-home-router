package handlers

import (
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
)

type rosterPagination struct {
	Kind, Target, Search, NextURL, PreviousURL string
	Offset                                     int
}

func pageRoster[T any](r *http.Request, kind string, items []T) ([]T, rosterPagination) {
	search := strings.TrimSpace(r.FormValue("search"))
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	start, end, next, previous := pickerWindow(len(items), offset)
	baseURL := "/api/v1/" + kind
	page := rosterPagination{Kind: kind, Target: kind + "-list", Search: search, Offset: start}
	if r.URL.Path == baseURL+"/deleted" {
		baseURL += "/deleted"
		page.Target = kind + "-deleted"
	}
	pageURL := func(offset int) string {
		return baseURL + "?" + url.Values{"search": {search}, "offset": {strconv.Itoa(offset)}}.Encode()
	}
	if next > 0 {
		page.NextURL = pageURL(next)
	}
	if start > 0 {
		page.PreviousURL = pageURL(previous)
	}
	return items[start:end], page
}

func pageRosterParticipants(r *http.Request, items []models.Participant) ([]models.Participant, rosterPagination) {
	return pageRoster(r, "participants", items)
}

func pageRosterDrivers(r *http.Request, items []models.Driver) ([]models.Driver, rosterPagination) {
	return pageRoster(r, "drivers", items)
}
