package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

type rosterPagination struct {
	Kind, Target, Search, NextURL, PreviousURL string
	Offset                                     int
}

type rosterReader struct {
	db database.DataStore
}

type rosterReadQuery struct {
	kind, search string
	offset       int
	deleted      bool
}

func rosterQuery(r *http.Request, kind string) rosterReadQuery {
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	return rosterReadQuery{
		kind: kind, search: strings.TrimSpace(r.FormValue("search")), offset: offset,
		deleted: r.URL.Path == "/api/v1/"+kind+"/deleted",
	}
}

func (q rosterReadQuery) window(total int) (int, int, rosterPagination) {
	start, end, next, previous := pickerWindow(total, q.offset)
	baseURL := "/api/v1/" + q.kind
	page := rosterPagination{Kind: q.kind, Target: q.kind + "-list", Search: q.search, Offset: start}
	if q.deleted {
		baseURL += "/deleted"
		page.Target = q.kind + "-deleted"
	}
	pageURL := func(offset int) string {
		return baseURL + "?" + url.Values{"search": {q.search}, "offset": {strconv.Itoa(offset)}}.Encode()
	}
	if next > 0 {
		page.NextURL = pageURL(next)
	}
	if start > 0 {
		page.PreviousURL = pageURL(previous)
	}
	return start, end, page
}

func (reader rosterReader) participantRows(r *http.Request) ([]models.Participant, rosterReadQuery, error) {
	query := rosterQuery(r, "participants")
	if query.deleted {
		rows, err := reader.db.Participants().ListDeleted(r.Context())
		return rows, query, err
	}
	rows, err := reader.db.Participants().List(r.Context(), query.search)
	return rows, query, err
}

func (reader rosterReader) driverRows(r *http.Request) ([]models.Driver, rosterReadQuery, error) {
	query := rosterQuery(r, "drivers")
	if query.deleted {
		rows, err := reader.db.Drivers().ListDeleted(r.Context())
		return rows, query, err
	}
	rows, err := reader.db.Drivers().List(r.Context(), query.search)
	return rows, query, err
}

func (reader rosterReader) participants(r *http.Request) (ParticipantListView, error) {
	rows, query, err := reader.participantRows(r)
	if err != nil {
		return ParticipantListView{}, err
	}
	labels, err := reader.db.Labels().List(r.Context())
	if err != nil {
		return ParticipantListView{}, err
	}
	labelIDs, err := reader.db.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		return ParticipantListView{}, err
	}
	start, end, page := query.window(len(rows))
	return ParticipantListView{Participants: rows[start:end], Pagination: page, Labels: labels, LabelIDs: labelIDs}, nil
}

func (reader rosterReader) drivers(r *http.Request) (DriverListView, error) {
	rows, query, err := reader.driverRows(r)
	if err != nil {
		return DriverListView{}, err
	}
	labels, err := reader.db.Labels().List(r.Context())
	if err != nil {
		return DriverListView{}, err
	}
	labelIDs, err := reader.db.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		return DriverListView{}, err
	}
	start, end, page := query.window(len(rows))
	return DriverListView{Drivers: rows[start:end], Pagination: page, Labels: labels, LabelIDs: labelIDs}, nil
}

func (reader rosterReader) participantJSON(r *http.Request) (ParticipantListResponse, error) {
	rows, _, err := reader.participantRows(r)
	if err != nil {
		return ParticipantListResponse{}, err
	}
	labelIDs, err := reader.db.Labels().ListLabelIDsForParticipants(r.Context())
	if err != nil {
		return ParticipantListResponse{}, err
	}
	responses := make([]ParticipantResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, ParticipantResponse{Participant: row, LabelIDs: append([]int64{}, labelIDs[row.ID]...)})
	}
	return ParticipantListResponse{Participants: responses, Total: len(rows)}, nil
}

func (reader rosterReader) driverJSON(r *http.Request) (DriverListResponse, error) {
	rows, _, err := reader.driverRows(r)
	if err != nil {
		return DriverListResponse{}, err
	}
	labelIDs, err := reader.db.Labels().ListLabelIDsForDrivers(r.Context())
	if err != nil {
		return DriverListResponse{}, err
	}
	responses := make([]DriverResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, DriverResponse{Driver: row, LabelIDs: append([]int64{}, labelIDs[row.ID]...)})
	}
	return DriverListResponse{Drivers: responses, Total: len(rows)}, nil
}
