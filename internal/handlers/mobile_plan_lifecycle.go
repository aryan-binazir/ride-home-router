package handlers

import (
	"context"
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
func (l mobilePlanLifecycle) EditInputsContext(ctx context.Context, id string, edit func(*mobilePlanInputs)) (plandraft.Draft, error) {
	displacedSessionID := ""
	draft, err := l.drafts.Edit(ctx, id, func(d *plandraft.Draft) {
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
	if err != nil {
		return plandraft.Draft{}, err
	}
	if err := l.sessions.DeleteContext(ctx, displacedSessionID); err != nil {
		return draft, err
	}
	return draft, nil
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
func (l mobilePlanLifecycle) AdoptCalculationContext(ctx context.Context, id string, original plandraft.Draft, sessionID string) (mobilePlanAdoption, error) {
	displaced, ok, err := l.drafts.Attach(ctx, id, original.Revision, sessionID)
	if err != nil {
		return mobilePlanExpired, err
	}
	if !ok {
		if err = l.sessions.DeleteContext(ctx, sessionID); err != nil {
			return mobilePlanExpired, err
		}
		current, found, err := l.drafts.Load(ctx, id)
		if err != nil {
			return mobilePlanExpired, err
		}
		if found && current.RouteSessionID != "" {
			_, live, err := l.sessions.Load(ctx, current.RouteSessionID)
			if err != nil {
				return mobilePlanExpired, err
			}
			if live {
				return mobilePlanSupersededLive, nil
			}
		}
		return mobilePlanExpired, nil
	}
	if err = l.sessions.DeleteContext(ctx, displaced); err != nil {
		return mobilePlanAdopted, err
	}
	return mobilePlanAdopted, nil
}

// ReleaseSavedSessionContext never detaches a newer calculation.
func (l mobilePlanLifecycle) ReleaseSavedSessionContext(ctx context.Context, id, sessionID string) error {
	_, err := l.drafts.Detach(ctx, id, sessionID)
	return err
}
