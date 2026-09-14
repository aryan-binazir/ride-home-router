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

func pageRoster[T any](r *http.Request, kind string, items []T, describe func(T) string) ([]T, rosterPagination) {
	search := strings.TrimSpace(r.FormValue("search"))
	matches := make([]T, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(describe(item)), strings.ToLower(search)) {
			matches = append(matches, item)
		}
	}
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	start, end, next, previous := pickerWindow(len(matches), offset)
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
	return matches[start:end], page
}

func pageRosterParticipants(r *http.Request, items []models.Participant) ([]models.Participant, rosterPagination) {
	return pageRoster(r, "participants", items, func(p models.Participant) string { return p.Name + " " + p.AddressName + " " + p.Address })
}

func pageRosterDrivers(r *http.Request, items []models.Driver) ([]models.Driver, rosterPagination) {
	return pageRoster(r, "drivers", items, func(d models.Driver) string { return d.Name + " " + d.AddressName + " " + d.Address })
}
