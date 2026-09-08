package handlers

import (
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
)

// mobilePlanInputs excludes session ownership and revision bookkeeping from edits.
type mobilePlanInputs struct {
	LocationID       int64
	ParticipantIDs   []int64
	DriverIDs        []int64
	DriverVehicleIDs map[int64]int64
	RouteTime        string
	Mode             string
}

// mobilePlanLifecycle owns the relationship between a draft's inputs and its routes.
// Calculation, persistence, and roster availability policy stay with their callers.
type mobilePlanLifecycle struct {
	drafts   *plandraft.Store
	sessions *routesession.Store
}

func (h *Handler) mobilePlan() mobilePlanLifecycle {
	return mobilePlanLifecycle{drafts: h.PlanDraft, sessions: h.RouteSession}
}

// EditInputs invalidates even an edit that leaves input values unchanged, as the
// existing pickers do. The callback runs under the draft lock and must only edit
// inputs. Session deletion happens after the draft lock has been released.
func (l mobilePlanLifecycle) EditInputs(id string, edit func(*mobilePlanInputs)) plandraft.Draft {
	displacedSessionID := ""
	draft := l.drafts.Update(id, func(d *plandraft.Draft) {
		inputs := mobilePlanInputs{
			LocationID: d.LocationID, ParticipantIDs: d.ParticipantIDs, DriverIDs: d.DriverIDs,
			DriverVehicleIDs: d.DriverVehicleIDs, RouteTime: d.RouteTime, Mode: d.Mode,
		}
		edit(&inputs)
		d.LocationID = inputs.LocationID
		d.ParticipantIDs = inputs.ParticipantIDs
		d.DriverIDs = inputs.DriverIDs
		d.DriverVehicleIDs = inputs.DriverVehicleIDs
		d.RouteTime = inputs.RouteTime
		d.Mode = inputs.Mode
		displacedSessionID = d.RouteSessionID
		d.RouteSessionID = ""
	})
	l.sessions.Delete(displacedSessionID)
	return draft
}

type mobilePlanAdoption uint8

const (
	mobilePlanAdopted mobilePlanAdoption = iota
	mobilePlanSupersededLive
	mobilePlanExpired
)

// AdoptCalculation accepts only a result for the original, unchanged draft.
// A losing result is deleted without touching a competing winner. Keep the
// existing failure-path reads: Get and Snapshot also refresh their stores' TTLs.
func (l mobilePlanLifecycle) AdoptCalculation(id string, original plandraft.Draft, sessionID string) mobilePlanAdoption {
	displacedSessionID, ok := l.drafts.SetRouteSessionIDIfUnchanged(id, original.Revision, sessionID)
	if !ok {
		l.sessions.Delete(sessionID)
		if currentDraft, found := l.drafts.Get(id); found && currentDraft.RouteSessionID != "" {
			if _, live := l.sessions.Snapshot(currentDraft.RouteSessionID); live {
				return mobilePlanSupersededLive
			}
		}
		return mobilePlanExpired
	}
	l.sessions.Delete(displacedSessionID)
	return mobilePlanAdopted
}

// ReleaseSavedSession is called only after a successful commit, with the session
// captured before that commit. A result attached during persistence stays attached.
func (l mobilePlanLifecycle) ReleaseSavedSession(id, consumedSessionID string) {
	l.drafts.ClearRouteSessionIDIfCurrent(id, consumedSessionID)
}
