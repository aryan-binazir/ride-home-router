package handlers

import (
	"net/http"
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

func newMobileBase(r *http.Request, title, activeTab, message string) mobileBaseView {
	notice := map[string]string{"participant": "Participant saved.", "driver": "Driver saved.", "location": "Location saved.", "van": "Van saved."}[r.URL.Query().Get("saved")]
	return mobileBaseView{Title: title, ActiveTab: activeTab, Error: message, Notice: notice}
}

type mobileErrorView struct {
	mobileBaseView
	Message       string
	ChangeDrivers bool
}

type mobilePlanView struct {
	mobileBaseView
	Draft                 plandraft.Draft
	RouteTimeDisplay      string
	Location              *models.ActivityLocation
	Participants          []models.Participant
	Drivers               []models.Driver
	LastEvent             *EventWithSummary
	SeatCount             int
	InvalidVanAssignments bool
}

type mobileLocationView struct {
	Search                 string
	Offset, Next, Previous int
	HiddenSelected         bool
	mobileBaseView
	Locations  []models.ActivityLocation
	SelectedID int64
}

type mobileRidersView struct {
	Offset, Next, Previous int
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
	Offset, Next, Previous int
	SelectedCapacities     map[int64]int
	mobileBaseView
	Drivers           []models.Driver
	Selected          map[int64]bool
	Vehicles          []models.OrganizationVehicle
	AssignedVehicles  map[int64]*models.OrganizationVehicle
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
	ActionsOnly bool
	Append      bool
	Index       int
	Route       models.CalculatedRoute
	DriverText  string
	ParentText  string
	ETAs        []string
	// Timing is this response's measurement for the car; nil Route means no numbers.
	Timing RouteTiming
}

type mobileRoutesView struct {
	Patch bool
	mobileBaseView
	Snapshot  routesession.Snapshot
	EventDate string
	Notes     string
	Routes    []mobileRoute
	// ShowAggregates is true only when every occupied car was measured in this response.
	ShowAggregates bool
	Attribution    bool
}

type mobilePeopleView struct {
	ParticipantNextURL, ParticipantPreviousURL, DriverNextURL, DriverPreviousURL string
	mobileBaseView
	Search            string
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
	Groups         []mobileHistoryGroup
	UseMiles       bool
	Total          int
	DisplayedCount int
	NextOffset     int
	PageSize       int
	LastMonth      string
}

type mobileHistoryGroup struct {
	Label     string
	HideLabel bool
	Events    []EventWithSummary
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
