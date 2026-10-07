package importer

import (
	"context"
	"encoding/json"
	"errors"
	"ride-home-router/internal/database"
)

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

func (s *Store) SelectRowsPatch(ctx context.Context, id string, patch map[int]bool) (ProgressSnapshot, error) {
	var result ProgressSnapshot
	err := s.records.Transact(ctx, "import", id, s.ttl, func(record *database.WorkflowRecord, w database.WorkflowWrites) error {
		var h importHeader
		if err := json.Unmarshal(record.Data, &h); err != nil {
			return err
		}
		if h.Status != StatusPreviewing || record.Consumed {
			return ErrInvalidSessionState
		}
		if err := w.PatchImportSelections(ctx, id, patch); err != nil {
			if errors.Is(err, database.ErrInvalidWorkflowSelection) {
				return ErrInvalidSelection
			}
			return err
		}
		counts, err := w.ImportSummary(ctx, id)
		if err != nil {
			return err
		}
		result = importProgressSnapshot(id, h.Status, counts)
		return nil
	})
	if errors.Is(err, database.ErrNotFound) {
		err = ErrSessionNotFound
	}
	if err != nil {
		return ProgressSnapshot{}, err
	}
	return result, nil
}

// CommitRowsPatch applies the visible page delta atomically with the commit.
func (s *Store) CommitRowsPatch(ctx context.Context, id string, patch map[int]bool) (CommitResult, error) {
	return s.commitPersistent(ctx, id, nil, patch)
}
