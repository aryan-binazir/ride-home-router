package importer

import (
	"context"
	"encoding/json"
	"ride-home-router/internal/database"
)

// applySelectionPatch validates the entire delta before changing any selection.
func applySelectionPatch(selected []bool, patch map[int]bool) error {
	for index := range patch {
		if index < 0 || index >= len(selected) {
			return ErrInvalidSelection
		}
	}
	for index, value := range patch {
		selected[index] = value
	}
	return nil
}

// SelectRowsPatch changes only submitted rows, under the session's write lock.
func (s *Store) SelectRowsPatch(ctx context.Context, id string, patch map[int]bool) (Snapshot, error) {
	if s.records == nil {
		state, err := s.lockSession(id)
		if err != nil {
			return Snapshot{}, err
		}
		defer state.mu.Unlock()
		if state.status != StatusPreviewing {
			return Snapshot{}, ErrInvalidSessionState
		}
		if err := applySelectionPatch(state.selected, patch); err != nil {
			return Snapshot{}, err
		}
		return snapshotOf(state), nil
	}
	err := s.records.Transact(ctx, "import", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		var h importHeader
		if err := json.Unmarshal(record.Data, &h); err != nil {
			return err
		}
		if h.Status != StatusPreviewing || record.Consumed {
			return ErrInvalidSessionState
		}
		stored, err := w.ImportRows(ctx, id)
		if err != nil {
			return err
		}
		_, selected, err := decodeImportRows(stored)
		if err != nil {
			return err
		}
		if err := applySelectionPatch(selected, patch); err != nil {
			return err
		}
		return w.SelectImportRows(ctx, id, selected)
	})
	if err != nil {
		return Snapshot{}, err
	}
	result, _, err := s.Load(ctx, id)
	return result, err
}

// CommitRowsPatch applies the visible page delta atomically with the commit.
func (s *Store) CommitRowsPatch(ctx context.Context, id string, patch map[int]bool) (CommitResult, error) {
	return s.commit(ctx, id, nil, patch)
}
