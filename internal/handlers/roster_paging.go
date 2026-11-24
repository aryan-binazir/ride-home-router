package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/models"
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

type rosterPage[T any] struct {
	rows       []T
	pagination rosterPagination
	labels     []models.Label
	labelIDs   map[int64][]int64
}

func (kind rosterKind[T, R]) rows(db database.DataStore, r *http.Request) ([]T, rosterReadQuery, error) {
	query := rosterQuery(r, kind.plural)
	repo := kind.repository(db)
	if query.deleted {
		rows, err := repo.ListDeleted(r.Context())
		return rows, query, err
	}
	rows, err := repo.List(r.Context(), query.search)
	return rows, query, err
}

func (kind rosterKind[T, R]) page(db database.DataStore, r *http.Request) (rosterPage[T], error) {
	rows, query, err := kind.rows(db, r)
	if err != nil {
		return rosterPage[T]{}, err
	}
	labels, err := db.Labels().List(r.Context())
	if err != nil {
		return rosterPage[T]{}, err
	}
	labelIDs, err := kind.labelIDs(db.Labels(), r.Context())
	if err != nil {
		return rosterPage[T]{}, err
	}
	start, end, page := query.window(len(rows))
	return rosterPage[T]{rows: rows[start:end], pagination: page, labels: labels, labelIDs: labelIDs}, nil
}

func (kind rosterKind[T, R]) listJSON(db database.DataStore, r *http.Request) (any, error) {
	rows, _, err := kind.rows(db, r)
	if err != nil {
		return nil, err
	}
	labelIDs, err := kind.labelIDs(db.Labels(), r.Context())
	if err != nil {
		return nil, err
	}
	response := make([]R, 0, len(rows))
	for i := range rows {
		id, _ := kind.identity(&rows[i])
		response = append(response, kind.response(&rows[i], append([]int64{}, labelIDs[id]...)))
	}
	return kind.jsonList(response), nil
}

func (reader rosterReader) participants(r *http.Request) (ParticipantListView, error) {
	page, err := participantRoster.page(reader.db, r)
	return participantListView(page), err
}

func (reader rosterReader) drivers(r *http.Request) (DriverListView, error) {
	page, err := driverRoster.page(reader.db, r)
	return driverListView(page), err
}

func participantListView(page rosterPage[models.Participant]) ParticipantListView {
	return ParticipantListView{Participants: page.rows, Pagination: page.pagination, Labels: page.labels, LabelIDs: page.labelIDs}
}

func driverListView(page rosterPage[models.Driver]) DriverListView {
	return DriverListView{Drivers: page.rows, Pagination: page.pagination, Labels: page.labels, LabelIDs: page.labelIDs}
}
