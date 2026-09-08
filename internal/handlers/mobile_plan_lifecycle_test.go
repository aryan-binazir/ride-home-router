package handlers

import (
	"errors"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"testing"
)

func TestMobilePlanLifecycleInputEditsInvalidateAttachedSession(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*mobilePlanInputs)
		check func(plandraft.Draft) bool
	}{
		{"location", func(d *mobilePlanInputs) { d.LocationID = 42 }, func(d plandraft.Draft) bool { return d.LocationID == 42 }},
		{"riders", func(d *mobilePlanInputs) { d.ParticipantIDs = []int64{42} }, func(d plandraft.Draft) bool { return len(d.ParticipantIDs) == 1 && d.ParticipantIDs[0] == 42 }},
		{"drivers", func(d *mobilePlanInputs) { d.DriverIDs = []int64{42} }, func(d plandraft.Draft) bool { return len(d.DriverIDs) == 1 && d.DriverIDs[0] == 42 }},
		{"vans", func(d *mobilePlanInputs) { d.DriverVehicleIDs[42] = 7 }, func(d plandraft.Draft) bool { return d.DriverVehicleIDs[42] == 7 }},
		{"time", func(d *mobilePlanInputs) { d.RouteTime = "19:00" }, func(d plandraft.Draft) bool { return d.RouteTime == "19:00" }},
		{"mode", func(d *mobilePlanInputs) { d.Mode = "pickup" }, func(d plandraft.Draft) bool { return d.Mode == "pickup" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drafts := plandraft.NewStore()
			t.Cleanup(drafts.Close)
			sessions := routesession.NewStore(routeEditDistanceCalculator{})
			t.Cleanup(sessions.Close)
			attached := sessions.Create(routesession.CreateInput{})
			unrelated := sessions.Create(routesession.CreateInput{})
			id := drafts.NewID()
			drafts.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = attached.ID })
			lifecycle := mobilePlanLifecycle{drafts: drafts, sessions: sessions}

			got := lifecycle.EditInputs(id, tt.edit)

			if !tt.check(got) || got.RouteSessionID != "" {
				t.Fatalf("edited draft = %#v, want updated input and no session", got)
			}
			stored, ok := drafts.Get(id)
			if !ok || !tt.check(stored) || stored.RouteSessionID != "" {
				t.Fatalf("stored draft = %#v, found=%t", stored, ok)
			}
			if _, live := sessions.Snapshot(attached.ID); live {
				t.Fatal("displaced session remains live")
			}
			if _, live := sessions.Snapshot(unrelated.ID); !live {
				t.Fatal("unrelated session was deleted")
			}
		})
	}
}

func TestMobilePlanLifecycleReleasePreservesNewerSession(t *testing.T) {
	drafts := plandraft.NewStore()
	t.Cleanup(drafts.Close)
	sessions := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(sessions.Close)
	lifecycle := mobilePlanLifecycle{drafts: drafts, sessions: sessions}
	consumed := sessions.Create(routesession.CreateInput{})
	id := drafts.NewID()
	original := drafts.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = consumed.ID })
	newer := sessions.Create(routesession.CreateInput{})
	if got := lifecycle.AdoptCalculation(id, original, newer.ID); got != mobilePlanAdopted {
		t.Fatalf("adoption = %v, want adopted", got)
	}

	lifecycle.ReleaseSavedSession(id, consumed.ID)

	current, ok := drafts.Get(id)
	if !ok || current.RouteSessionID != newer.ID {
		t.Fatalf("draft = %#v, want newer session attached", current)
	}
	if _, live := sessions.Snapshot(newer.ID); !live {
		t.Fatal("newer session was deleted")
	}
	lifecycle.ReleaseSavedSession(id, newer.ID)
	current, ok = drafts.Get(id)
	if !ok || current.RouteSessionID != "" {
		t.Fatalf("draft = %#v, want consumed session detached", current)
	}
}

func TestMobilePlanLifecycleRejectsResultWithoutLiveWinner(t *testing.T) {
	for _, state := range []string{"missing draft", "edited inputs", "deleted winner"} {
		t.Run(state, func(t *testing.T) {
			drafts := plandraft.NewStore()
			t.Cleanup(drafts.Close)
			sessions := routesession.NewStore(routeEditDistanceCalculator{})
			t.Cleanup(sessions.Close)
			lifecycle := mobilePlanLifecycle{drafts: drafts, sessions: sessions}
			id := drafts.NewID()
			original := plandraft.Draft{}
			if state != "missing draft" {
				original = lifecycle.EditInputs(id, func(d *mobilePlanInputs) { d.RouteTime = "18:30" })
				if state == "edited inputs" {
					lifecycle.EditInputs(id, func(d *mobilePlanInputs) { d.RouteTime = "19:00" })
				} else {
					winner := sessions.Create(routesession.CreateInput{})
					if got := lifecycle.AdoptCalculation(id, original, winner.ID); got != mobilePlanAdopted {
						t.Fatalf("adoption = %v, want adopted", got)
					}
					sessions.Delete(winner.ID)
				}
			}
			loser := sessions.Create(routesession.CreateInput{})

			if got := lifecycle.AdoptCalculation(id, original, loser.ID); got != mobilePlanExpired {
				t.Fatalf("adoption = %v, want expired when no live winner remains", got)
			}
			if _, live := sessions.Snapshot(loser.ID); live {
				t.Fatal("rejected result remains live")
			}
			current, found := drafts.Get(id)
			if state == "missing draft" && found {
				t.Fatal("adoption recreated a missing draft")
			}
			if state == "edited inputs" && (!found || current.RouteTime != "19:00" || current.RouteSessionID != "") {
				t.Fatalf("draft = %#v, want edited inputs without the stale result", current)
			}
		})
	}
}

func TestMobileSaveFailureKeepsDraftSessionForRetry(t *testing.T) {
	handler, store := newTestEventHandler(t, false)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	session := handler.RouteSession.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{{
			Driver:            &models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 2},
			EffectiveCapacity: 2,
			Stops:             []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider"}}},
			Mode:              models.RouteModeDropoff,
		}},
		Mode: models.RouteModeDropoff,
	})
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	handler.DB = eventRepositoryDataStore{
		DataStore: store,
		events:    failingEventRepository{EventRepository: store.Events(), err: errors.New("persistence failed")},
	}

	response := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"event_date": {"2026-09-07"}}, handler.HandleMobileSave)
	assertMobileRedirect(t, response, "/m/routes?error="+url.QueryEscape(messageGenericInternalError))
	current, ok := handler.PlanDraft.Get(id)
	if !ok || current.RouteSessionID != session.ID {
		t.Fatalf("draft after failed save = %#v, want captured session still attached", current)
	}
	if _, live := handler.RouteSession.Snapshot(session.ID); !live {
		t.Fatal("failed save consumed the route session")
	}
}
