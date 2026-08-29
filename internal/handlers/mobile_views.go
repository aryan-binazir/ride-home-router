package handlers

import (
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
)

type mobileBaseView struct {
	Title     string
	ActiveTab string
	Error     string
	Notice    string
}

func newMobileBase(title, activeTab, message string) mobileBaseView {
	return mobileBaseView{Title: title, ActiveTab: activeTab, Error: message}
}

type mobileErrorView struct {
	mobileBaseView
	Message string
}

type mobilePlanView struct {
	mobileBaseView
	Draft            plandraft.Draft
	RouteTimeDisplay string
	Location         *models.ActivityLocation
	Participants     []models.Participant
	Drivers          []models.Driver
	LastEvent        *EventWithSummary
	SeatCount        int
}

type mobileLocationView struct {
	mobileBaseView
	Locations  []models.ActivityLocation
	SelectedID int64
}

type mobileRidersView struct {
	mobileBaseView
	Participants      []models.Participant
	Selected          map[int64]bool
	Labels            []models.Label
	LabelIDs          map[int64][]int64
	Search            string
	LabelID           int64
	HiddenSelectedIDs []int64
}

type mobileDriversView struct {
	mobileBaseView
	Drivers           []models.Driver
	Selected          map[int64]bool
	Vehicles          []models.OrganizationVehicle
	Assignments       map[int64]int64
	SelectedSeats     int
	Labels            []models.Label
	LabelIDs          map[int64][]int64
	Search            string
	LabelID           int64
	HiddenSelectedIDs []int64
}

type mobileWhenView struct {
	mobileBaseView
	RouteTime string
	Mode      string
}

type mobileRoute struct {
	Index      int
	Route      models.CalculatedRoute
	DriverText string
	ParentText string
	ETAs       []string
}

type mobileRoutesView struct {
	mobileBaseView
	Snapshot routesession.Snapshot
	Routes   []mobileRoute
}

type mobilePeopleView struct {
	mobileBaseView
	Participants      []models.Participant
	Drivers           []models.Driver
	Labels            []models.Label
	ParticipantLabels map[int64][]int64
	DriverLabels      map[int64][]int64
}

type mobilePersonFormView struct {
	mobileBaseView
	Kind            string
	Action          string
	Name            string
	Address         string
	AddressName     string
	VehicleCapacity int
	Labels          []models.Label
	Selected        map[int64]bool
}

type mobilePlacesView struct {
	mobileBaseView
	Locations []models.ActivityLocation
	Vans      []models.OrganizationVehicle
}

type mobilePlaceFormView struct {
	mobileBaseView
	Kind     string
	Action   string
	Name     string
	Address  string
	Capacity int
}

type mobileHistoryView struct {
	mobileBaseView
	Groups   []mobileHistoryGroup
	UseMiles bool
}

type mobileHistoryGroup struct {
	Label  string
	Events []EventWithSummary
}

type mobileHistoryDetailView struct {
	mobileBaseView
	Event    *models.Event
	Routes   []mobileSavedRoute
	Summary  *models.EventSummary
	UseMiles bool
}

type mobileSavedRoute struct {
	Route      models.EventRoute
	DriverText string
	ParentText string
}
