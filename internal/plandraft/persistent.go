package plandraft

import (
	"context"
	"encoding/json"
	"errors"
	"ride-home-router/internal/database"
	"time"
)

// NewPersistentStore keeps all cross-request draft state in shared storage.
func NewPersistentStore(records database.WorkflowRepository) *Store {
	if records == nil {
		panic("plandraft: workflow repository is required")
	}
	return &Store{records: records, now: time.Now, ttl: defaultTTL}
}

func (s *Store) CreateDraft(ctx context.Context) (string, Draft, error) {
	id := s.NewID()
	if s.records == nil {
		return id, s.Update(id, func(*Draft) {}), nil
	}
	draft := defaultDraft(s.now())
	draft.Revision = 1
	data, err := json.Marshal(draft)
	if err != nil {
		return "", Draft{}, err
	}
	err = s.records.Create(ctx, "draft", id, data, s.ttl)
	return id, draft, err
}

func (s *Store) Load(ctx context.Context, id string) (Draft, bool, error) {
	if s.records == nil {
		d, ok := s.Get(id)
		return d, ok, nil
	}
	record, err := s.records.Load(ctx, "draft", id, s.ttl)
	if errors.Is(err, database.ErrNotFound) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, err
	}
	var d Draft
	err = json.Unmarshal(record.Data, &d)
	return d, err == nil, err
}

func (s *Store) Edit(ctx context.Context, id string, update func(*Draft)) (Draft, error) {
	if s.records == nil {
		return s.Update(id, update), nil
	}
	var result Draft
	err := s.records.Update(ctx, "draft", id, s.ttl, func(r *database.WorkflowRecord) error {
		if err := json.Unmarshal(r.Data, &result); err != nil {
			return err
		}
		revision := result.Revision
		update(&result)
		boundDraftSelections(&result)
		result.Revision = revision + 1
		data, err := json.Marshal(result)
		r.Data = data
		return err
	})
	return clone(result), err
}

func (s *Store) Attach(ctx context.Context, id string, revision uint64, sessionID string) (string, bool, error) {
	if s.records == nil {
		old, ok := s.SetRouteSessionIDIfUnchanged(id, revision, sessionID)
		return old, ok, nil
	}
	old := ""
	err := s.records.Update(ctx, "draft", id, s.ttl, func(r *database.WorkflowRecord) error {
		var d Draft
		if err := json.Unmarshal(r.Data, &d); err != nil {
			return err
		}
		if d.Revision != revision {
			return database.ErrWorkflowConflict
		}
		old = d.RouteSessionID
		d.RouteSessionID = sessionID
		d.Revision++
		data, err := json.Marshal(d)
		r.Data = data
		return err
	})
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrWorkflowConflict) {
		return "", false, nil
	}
	return old, err == nil, err
}

func (s *Store) Detach(ctx context.Context, id, sessionID string) (bool, error) {
	if s.records == nil {
		return s.ClearRouteSessionIDIfCurrent(id, sessionID), nil
	}
	changed := false
	err := s.records.Update(ctx, "draft", id, s.ttl, func(r *database.WorkflowRecord) error {
		var d Draft
		if err := json.Unmarshal(r.Data, &d); err != nil {
			return err
		}
		if sessionID == "" || d.RouteSessionID != sessionID {
			return database.ErrWorkflowConflict
		}
		d.RouteSessionID = ""
		d.Revision++
		data, err := json.Marshal(d)
		r.Data = data
		changed = true
		return err
	})
	if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrWorkflowConflict) {
		return false, nil
	}
	return changed, err
}
